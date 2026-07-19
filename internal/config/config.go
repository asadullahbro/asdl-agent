package config

import (
    "fmt"
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

    // Validate
    if cfg.HubURL == "" {
        return nil, fmt.Errorf("hub_url is required (set in config or ASDL_HUB_URL env)")
    }
    if cfg.VPNIP == "" {
        return nil, fmt.Errorf("vpn_ip is required (set in config or ASDL_VPN_IP env)")
    }

    return cfg, nil
}