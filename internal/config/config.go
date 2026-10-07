package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath" // Add this
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	HubURL   string        `yaml:"hub_url"`
	VPNIP    string        `yaml:"vpn_ip"`
	Interval time.Duration `yaml:"interval"`
	WorkDir  string        `yaml:"work_dir"`
	MaxJobs  int           `yaml:"max_jobs"`
	Enrolled bool          `yaml:"enrolled"`
	Dashboard DashboardConfig `yaml:"dashboard"`
}
type DashboardConfig struct {
    Port int `yaml:"port"`
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

// IsEnrolled checks if the node is already enrolled. The node keeps no ID of
// its own: the Hub knows which node it is from its mesh address.
func (c *Config) IsEnrolled() bool {
	return c.Enrolled && c.VPNIP != "" && c.HubURL != ""
}

// nodeIDLine matches a top-level "node_id:" key (plain or quoted).
var nodeIDLine = regexp.MustCompile(`^(?:node_id|"node_id"|'node_id')[ \t]*:`)

// RemoveNodeID deletes the node_id line from the config file at path and
// reports whether there was one. Older agents saved the node ID the Hub gave
// them here; the Hub now keeps it, and identifies a node by its mesh address,
// so the node holds none. Everything else in the file (other keys, comments,
// order, permissions) is left exactly as it was. A missing file is not an error.
func RemoveNodeID(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	var kept strings.Builder
	removed := false
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if nodeIDLine.MatchString(line) {
			removed = true
			continue
		}
		kept.WriteString(line)
	}
	if !removed {
		return false, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	// Write beside the file and rename over it, so a crash can't leave a
	// half-written config behind.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(kept.String()), info.Mode().Perm()); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp, info.Mode().Perm()); err != nil {
		os.Remove(tmp)
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return false, err
	}
	return true, nil
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
