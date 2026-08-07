package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/asdl/agent/internal/client"
	"github.com/asdl/agent/internal/config"
	"github.com/asdl/agent/internal/dashboard"
	"github.com/asdl/agent/internal/enrollment"
	"github.com/asdl/agent/internal/failover"
	"github.com/asdl/agent/internal/monitor"
	"github.com/asdl/agent/internal/runner"
	"github.com/asdl/agent/pkg/models"
)

var Version = "dev"

func main() {
	configPath := flag.String("config", config.DefaultConfigPath, "Path to config file")
	hubURL := flag.String("hub-url", "", "Hub URL (overrides config)")
	vpnIP := flag.String("vpn-ip", "", "VPN IP (overrides config)")
	enroll := flag.Bool("enroll", false, "Run enrollment flow")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Printf("Config load failed: %v", err)
		cfg = &config.Config{}
	}

	if *hubURL != "" {
		cfg.HubURL = *hubURL
	}
	if *vpnIP != "" {
		cfg.VPNIP = *vpnIP
	}

	// Run enrollment if flagged or not yet enrolled
	if *enroll || !cfg.IsEnrolled() {
		cfg, err = enrollment.Run(*configPath)
		if err != nil {
			log.Fatalf("Enrollment failed: %v", err)
		}
		// Wait for WireGuard to stabilize
		log.Println("⏳ Waiting for WireGuard mesh to stabilize...")
		time.Sleep(3 * time.Second)
	}

	log.Printf("Starting ASDL Agent %s", Version)
	log.Printf("Hub URL: %s", cfg.HubURL)
	log.Printf("VPN IP: %s", cfg.VPNIP)
	log.Printf("Node ID: %s", cfg.NodeID)
	log.Printf("Work Dir: %s", cfg.WorkDir)

	mon := monitor.NewMonitor()
	rnr := runner.NewRunner(cfg.WorkDir)
	cli := client.NewClient(cfg.HubURL, cfg.VPNIP)

	// Initialize job history and dashboard
	jobHistory := dashboard.NewRingBuffer(20)
	dash := dashboard.New(mon, jobHistory, cfg.HubURL, cfg.NodeID, cfg.VPNIP, Version)
	go dash.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal")
		cancel()
	}()

	if err := run(ctx, cfg, mon, rnr, cli, jobHistory); err != nil {
		log.Fatalf("Agent failed: %v", err)
	}

	log.Println("Agent stopped")
}

func run(ctx context.Context, cfg *config.Config,
	mon *monitor.Monitor, rnr *runner.Runner, cli *client.Client,
	jobHistory *dashboard.RingBuffer) error {

	// Get system info and register
	info, err := mon.GetSystemInfo()
	if err != nil {
		return err
	}
	info.VPNIP = cfg.VPNIP
	info.Version = Version

	if err := cli.Register(info); err != nil {
		return err
	}
	log.Printf("Registered with hub (Node ID: %s)", cli.GetNodeID())

	heartbeatTicker := time.NewTicker(cfg.Interval)
	defer heartbeatTicker.Stop()

	jobTicker := time.NewTicker(5 * time.Second)
	defer jobTicker.Stop()

	// Check for updates every 5 minutes
	updateTicker := time.NewTicker(5 * time.Minute)
	defer updateTicker.Stop()

	// Also check immediately on startup
	go selfUpdate(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-updateTicker.C:
			go selfUpdate(ctx)

		case <-heartbeatTicker.C:
			heartbeat, err := mon.GetHeartbeat()
			if err != nil {
				log.Printf("Failed to get heartbeat: %v", err)
				continue
			}
			if err := cli.SendHeartbeat(heartbeat); err != nil {
				log.Printf("Heartbeat failed: %v", err)
				continue
			}
			log.Printf("Heartbeat sent: CPU=%.1f%%, Mem=%dMB/%dMB",
				heartbeat.CPUPercent,
				heartbeat.MemoryUsed/1024/1024,
				heartbeat.MemoryTotal/1024/1024)

		case <-jobTicker.C:
			job, err := cli.ClaimJob()
			if err != nil {
				log.Printf("Failed to claim job: %v", err)
				continue
			}
			if job == nil {
				continue
			}

			log.Printf("Claimed job: %s (%s)", job.ID, job.Type)

			var result *models.JobResult
			switch job.Type {
			case "failover_start", "migrate_start":
				result = failover.Execute(ctx, job)
			default:
				result, err = rnr.Execute(ctx, job)
				if err != nil {
					log.Printf("Job execution failed: %v", err)
					result = &models.JobResult{
						JobID:    job.ID,
						Status:   "failed",
						Logs:     err.Error(),
						ExitCode: -1,
					}
				}
			}

			// Push to job history before completing
			jobHistory.Push(dashboard.JobEntry{
				ID:        job.ID,
				Type:      job.Type,
				Status:    result.Status,
				CreatedAt: job.CreatedAt,
				EndedAt:   time.Now(),
				ExitCode:  result.ExitCode,
			})

			if err := cli.CompleteJob(result); err != nil {
				log.Printf("Failed to report job completion: %v", err)
			} else {
				log.Printf("Job %s completed with status: %s", job.ID, result.Status)
			}

			// Self-restart after successful agent update
			if job.Type == "agent_update" && result.Status == "completed" {
				log.Println("🔄 Agent update complete, restarting process...")
				time.Sleep(1 * time.Second)
				if err := syscall.Exec(os.Args[0], os.Args, os.Environ()); err != nil {
					log.Printf("⚠️ Self-restart failed: %v", err)
				}
			}
		}
	}
}

func selfUpdate(ctx context.Context) {
	latest, err := getLatestVersion()
	if err != nil {
		log.Printf("⚠️ Version check failed: %v", err)
		return
	}

	if latest == Version {
		log.Printf("✅ Agent is up to date (%s)", Version)
		return
	}

	log.Printf("🔄 New version available: %s (current: %s), updating...", latest, Version)

	binary := "asdl-agent-linux"
	if runtime.GOOS == "darwin" {
		binary = "asdl-agent-mac"
	}

	url := fmt.Sprintf("https://github.com/asadullahbro/asdl-agent/releases/latest/download/%s", binary)
	checksumURL := "https://github.com/asadullahbro/asdl-agent/releases/latest/download/checksums.txt"

	// Download
	if out, err := exec.CommandContext(ctx, "curl", "-fsSL", url, "-o", "/tmp/asdl-agent-new").CombinedOutput(); err != nil {
		log.Printf("⚠️ Download failed: %v\n%s", err, out)
		return
	}

	// Verify checksum
	expected, err := exec.CommandContext(ctx, "sh", "-c",
		fmt.Sprintf(`curl -fsSL %s | grep "%s" | awk '{print $1}'`, checksumURL, binary)).Output()
	if err != nil {
		log.Printf("⚠️ Checksum fetch failed: %v", err)
		return
	}

	actual, err := exec.CommandContext(ctx, "sh", "-c", "sha256sum /tmp/asdl-agent-new | awk '{print $1}'").Output()
	if err != nil {
		log.Printf("⚠️ Checksum compute failed: %v", err)
		return
	}

	if strings.TrimSpace(string(expected)) != strings.TrimSpace(string(actual)) {
		log.Printf("❌ Checksum mismatch, aborting update")
		os.Remove("/tmp/asdl-agent-new")
		return
	}

	// Replace binary
	if out, err := exec.CommandContext(ctx, "sh", "-c",
		"chmod +x /tmp/asdl-agent-new && sudo mv /tmp/asdl-agent-new "+os.Args[0]).CombinedOutput(); err != nil {
		log.Printf("⚠️ Binary replace failed: %v\n%s", err, out)
		return
	}

	log.Printf("✅ Updated to %s, restarting...", latest)
	time.Sleep(1 * time.Second)
	syscall.Exec(os.Args[0], os.Args, os.Environ())
}

func getLatestVersion() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/asadullahbro/asdl-agent/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}

	return release.TagName, nil
}