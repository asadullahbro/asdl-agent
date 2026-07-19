package main

import (
    "context"
    "flag"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/asdl/agent/internal/client"
    "github.com/asdl/agent/internal/config"
    "github.com/asdl/agent/internal/monitor"
    "github.com/asdl/agent/internal/runner"
    "github.com/asdl/agent/internal/failover"
    "github.com/asdl/agent/pkg/models"
)

func main() {
    configPath := flag.String("config", "config.yaml", "Path to config file")
    hubURL := flag.String("hub-url", "", "Hub URL (overrides config)")
    vpnIP := flag.String("vpn-ip", "", "VPN IP (overrides config)")
    flag.Parse()

    cfg, err := config.Load(*configPath)
    if err != nil {
        log.Fatalf("Failed to load config: %v", err)
    }

    // Override with CLI flags if provided
    if *hubURL != "" {
        cfg.HubURL = *hubURL
    }
    if *vpnIP != "" {
        cfg.VPNIP = *vpnIP
    }

    log.Printf("Starting ASDL Agent")
    log.Printf("Hub URL: %s", cfg.HubURL)
    log.Printf("VPN IP: %s", cfg.VPNIP)
    log.Printf("Work Dir: %s", cfg.WorkDir)

    // Initialize components
    mon := monitor.NewMonitor()
    rnr := runner.NewRunner(cfg.WorkDir)
    cli := client.NewClient(cfg.HubURL, cfg.VPNIP)  // Pass both args

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Handle signals
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    go func() {
        <-sigChan
        log.Println("Received shutdown signal")
        cancel()
    }()

    if err := run(ctx, cfg, mon, rnr, cli); err != nil {
        log.Fatalf("Agent failed: %v", err)
    }

    log.Println("Agent stopped")
}

func run(ctx context.Context, cfg *config.Config,
    mon *monitor.Monitor, rnr *runner.Runner, cli *client.Client) error {

    // Get system info and register
    info, err := mon.GetSystemInfo()
    if err != nil {
        return err
    }
    info.VPNIP = cfg.VPNIP

    if err := cli.Register(info); err != nil {
        return err
    }
    log.Printf("Registered with hub (Node ID: %s)", cli.GetNodeID())

    heartbeatTicker := time.NewTicker(cfg.Interval)
    defer heartbeatTicker.Stop()

    jobTicker := time.NewTicker(5 * time.Second)
    defer jobTicker.Stop()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()

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
                }
            if err != nil {
                log.Printf("Job execution failed: %v", err)
                result = &models.JobResult{
                    JobID:    job.ID,
                    Status:   "failed",
                    Logs:     err.Error(),
                    ExitCode: -1,
                }
            }

            if err := cli.CompleteJob(result); err != nil {
                log.Printf("Failed to report job completion: %v", err)
            } else {
                log.Printf("Job %s completed with status: %s", job.ID, result.Status)
            }
        }
    }
}