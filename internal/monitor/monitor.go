package monitor

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"

	"github.com/asdl/agent/pkg/models"
)

type Monitor struct {
	lastCPU time.Time
	hubIP   string
}

func NewMonitor(hubIP string) *Monitor {
	return &Monitor{
		lastCPU: time.Now(),
		hubIP:   hubIP,
	}
}
func (m *Monitor) GetSystemInfo() (*models.NodeInfo, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return nil, fmt.Errorf("failed to get memory info: %w", err)
	}

	diskInfo, err := disk.Usage("/")
	if err != nil {
		return nil, fmt.Errorf("failed to get disk info: %w", err)
	}

	cpuCount, err := cpu.Counts(true)
	if err != nil {
		cpuCount = runtime.NumCPU()
	}

	return &models.NodeInfo{
		Hostname:     hostname,
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		CPU:          cpuCount,
		MemoryTotal:  int64(memInfo.Total),
		DiskTotal:    int64(diskInfo.Total),
		Capabilities: m.detectCapabilities(),
	}, nil
}

// GetHeartbeat returns current resource usage plus link-quality metrics
// (ping latency to the Hub and WiFi signal strength, if applicable).
func (m *Monitor) GetHeartbeat() (*models.Heartbeat, error) {
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return nil, fmt.Errorf("failed to get memory info: %w", err)
	}

	diskInfo, err := disk.Usage("/")
	if err != nil {
		return nil, fmt.Errorf("failed to get disk info: %w", err)
	}

	// Try to get CPU percent, fallback to 0 if it fails
	var cpuPercent float64 = 0
	cpuPercents, err := cpu.Percent(100*time.Millisecond, false)
	if err == nil && len(cpuPercents) > 0 {
		cpuPercent = cpuPercents[0]
	}
	// If err != nil, cpuPercent stays 0

	loadAvg, err := load.Avg()
	if err != nil {
		loadAvg = &load.AvgStat{}
	}

	pingLatency := m.getPingLatency(m.hubIP)
	wifiSignal := m.getWiFiSignal()

	return &models.Heartbeat{
		CPUPercent:  cpuPercent,
		MemoryUsed:  int64(memInfo.Used),
		MemoryTotal: int64(memInfo.Total),
		DiskUsed:    int64(diskInfo.Used),
		DiskTotal:   int64(diskInfo.Total),
		Uptime:      m.getUptime(),
		LoadAvg1:    loadAvg.Load1,
		LoadAvg5:    loadAvg.Load5,
		LoadAvg15:   loadAvg.Load15,
		PingLatency: pingLatency,
		WiFiSignal:  wifiSignal,
		Timestamp:   time.Now(),
	}, nil
}

func (m *Monitor) detectCapabilities() []string {
	caps := []string{"bash"}

	// Check for Docker
	if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		caps = append(caps, "docker")
	}

	// Check for Git
	if _, err := os.Stat("/usr/bin/git"); err == nil {
		caps = append(caps, "git")
	} else if _, err := os.Stat("/usr/local/bin/git"); err == nil {
		caps = append(caps, "git")
	}

	// Check for Python
	if _, err := os.Stat("/usr/bin/python3"); err == nil {
		caps = append(caps, "python")
	}

	// Check for Node.js
	if _, err := os.Stat("/usr/bin/node"); err == nil {
		caps = append(caps, "node")
	}

	// Check for Go
	if _, err := os.Stat("/usr/bin/go"); err == nil {
		caps = append(caps, "go")
	}

	// Check for Docker Compose
	if _, err := os.Stat("/usr/bin/docker-compose"); err == nil {
		caps = append(caps, "docker-compose")
	}

	return caps
}

func (m *Monitor) getUptime() int64 {
    if runtime.GOOS == "darwin" {
        out, err := exec.Command("sysctl", "-n", "kern.boottime").Output()
        if err != nil {
            return 0
        }
        // output: { sec = 1234567890, usec = 0 } Mon Jan ...
        s := string(out)
        idx := strings.Index(s, "sec = ")
        if idx == -1 {
            return 0
        }
        s = s[idx+6:]
        end := strings.IndexAny(s, ", }")
        if end == -1 {
            return 0
        }
        bootSec, err := strconv.ParseInt(strings.TrimSpace(s[:end]), 10, 64)
        if err != nil {
            return 0
        }
        return time.Now().Unix() - bootSec
    }

    // Linux
    data, err := os.ReadFile("/proc/uptime")
    if err != nil {
        return 0
    }
    parts := strings.Fields(string(data))
    if len(parts) == 0 {
        return 0
    }
    uptime, err := strconv.ParseFloat(parts[0], 64)
    if err != nil {
        return 0
    }
    return int64(uptime)
}

func (m *Monitor) getPingLatency(hubIP string) float64 {
	// Ping the Hub (10.100.0.1)
	cmd := exec.Command("ping", "-c", "3", "-W", "2", hubIP)
	output, err := cmd.Output()
	if err != nil {
		return 999.0 // High latency means unreachable
	}

	// Parse ping output for average latency
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "avg") {
			// Extract avg latency from: "rtt min/avg/max/mdev = 0.123/0.456/0.789/0.012 ms"
			parts := strings.Split(line, "=")
			if len(parts) > 1 {
				stats := strings.Split(strings.TrimSpace(parts[1]), "/")
				if len(stats) > 1 {
					val, _ := strconv.ParseFloat(stats[1], 64)
					return val
				}
			}
		}
	}

	return 0
}

func (m *Monitor) getWiFiSignal() int {
	// Get WiFi signal strength (Linux)
	if runtime.GOOS == "linux" {
		// Try iwconfig
		cmd := exec.Command("iwconfig", "wlan0")
		output, err := cmd.Output()
		if err == nil {
			lines := strings.Split(string(output), "\n")
			for _, line := range lines {
				if strings.Contains(line, "Signal level") {
					// Extract signal value
					parts := strings.Split(line, "=")
					if len(parts) > 1 {
						val := strings.TrimSpace(strings.Split(parts[1], " ")[0])
						// Convert dBm to percentage (rough estimate)
						dbm, _ := strconv.Atoi(val)
						// Convert -100 to 0, -30 to 100
						signal := (dbm + 100) * 100 / 70
						if signal > 100 {
							signal = 100
						}
						if signal < 0 {
							signal = 0
						}
						return signal
					}
				}
			}
		}
	}

	return 100 // Default if can't detect
}
