package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath" // Add this
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	HubURL   string        `yaml:"hub_url"`
	VPNIP    string        `yaml:"vpn_ip"`
	NodeID   string        `yaml:"node_id"`
	Interval time.Duration `yaml:"interval"`
	WorkDir  string        `yaml:"work_dir"`
	MaxJobs  int           `yaml:"max_jobs"`
	Enrolled bool          `yaml:"enrolled"` // Added Enrolled field
}

const DefaultConfigPath = "/etc/asdl/agent.conf"

func Load(path string) (*Config, error) {
	cfg := &Config{
		Interval: 30 * time.Second,
		WorkDir:  "/tmp/asdl",
		MaxJobs:  5,
		Enrolled: false,
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
	if nodeID := os.Getenv("ASDL_NODE_ID"); nodeID != "" {
		cfg.NodeID = nodeID
	}
	if dir := os.Getenv("ASDL_WORK_DIR"); dir != "" {
		cfg.WorkDir = dir
	}

	// Auto-detect WireGuard IP if not set
	if cfg.VPNIP == "" {
		cfg.VPNIP = detectWireGuardIP()
	}

	return cfg, nil
}

// Save writes the config to the specified path
func (c *Config) Save(path string) error {
	// Create directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// IsEnrolled checks if the node is already enrolled
func (c *Config) IsEnrolled() bool {
	return c.Enrolled && c.NodeID != "" && c.VPNIP != "" && c.HubURL != ""
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
