package config

import (
    "fmt"
    "net"
    "os"
    "time"

    "gopkg.in/yaml.v3"
)

type Config struct {
    HubURL   string        `yaml:"hub_url"`
    VPNIP    string        `yaml:"vpn_ip"`
    Interval time.Duration `yaml:"interval"`
    WorkDir  string        `yaml:"work_dir"`
    MaxJobs  int           `yaml:"max_jobs"`
}

func Load(path string) (*Config, error) {
    cfg := &Config{
        Interval: 30 * time.Second,
        WorkDir:  "/tmp/asdl",
        MaxJobs:  5,
    }

    // Load from file if exists
    if _, err := os.Stat(path); err == nil {
        data, err := os.ReadFile(path)
        if err != nil {
            return nil, fmt.Errorf("failed to read config: %w", err)
        }
        if err := yaml.Unmarshal(data, cfg); err != nil {
            return nil, fmt.Errorf("failed to parse config: %w", err)
        }
    }

    // Environment overrides
    if url := os.Getenv("ASDL_HUB_URL"); url != "" {
        cfg.HubURL = url
    }
    if ip := os.Getenv("ASDL_VPN_IP"); ip != "" {
        cfg.VPNIP = ip
    }
    if dir := os.Getenv("ASDL_WORK_DIR"); dir != "" {
        cfg.WorkDir = dir
    }

    // Auto-detect WireGuard IP if not set
    if cfg.VPNIP == "" {
        cfg.VPNIP = detectWireGuardIP()
    }

    // Validate
    if cfg.HubURL == "" {
        return nil, fmt.Errorf("hub_url is required (set in config or ASDL_HUB_URL env)")
    }
    if cfg.VPNIP == "" {
        return nil, fmt.Errorf("vpn_ip could not be detected, set it in config or ASDL_VPN_IP env")
    }

    return cfg, nil
}

func detectWireGuardIP() string {
    // Try common WireGuard interface names
    ifaces := []string{"wg0", "wg1", "asdl0"}

    for _, name := range ifaces {
        iface, err := net.InterfaceByName(name)
        if err != nil {
            continue
        }

        addrs, err := iface.Addrs()
        if err != nil {
            continue
        }

        for _, addr := range addrs {
            var ip net.IP
            switch v := addr.(type) {
            case *net.IPNet:
                ip = v.IP
            case *net.IPAddr:
                ip = v.IP
            }

            if ip != nil && ip.To4() != nil {
                return ip.String()
            }
        }
    }

    return ""
}