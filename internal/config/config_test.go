package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.conf")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The config the installer writes, with the line older agents kept.
const installerConfig = `hub_url: http://10.101.0.1:8080
node_id: 4f1c2f4e-1b6a-4d0e-9a55-0d7a5d1b9c11
vpn_ip: 10.101.0.4
enrolled: true
interval: 30s
work_dir: /tmp/asdl-hub-asdl-web
max_jobs: 5
dashboard:
  port: 8081
`

func TestRemoveNodeID(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		removed bool
	}{
		{"installer config", installerConfig, `hub_url: http://10.101.0.1:8080
vpn_ip: 10.101.0.4
enrolled: true
interval: 30s
work_dir: /tmp/asdl-hub-asdl-web
max_jobs: 5
dashboard:
  port: 8081
`, true},
		{"quoted key", "node_id: x\nvpn_ip: 1\n", "vpn_ip: 1\n", true},
		{"double-quoted key", "\"node_id\": x\nvpn_ip: 1\n", "vpn_ip: 1\n", true},
		{"space before the colon", "node_id : x\nvpn_ip: 1\n", "vpn_ip: 1\n", true},
		{"last line without a newline", "vpn_ip: 1\nnode_id: x", "vpn_ip: 1\n", true},
		{"windows line endings", "hub_url: h\r\nnode_id: x\r\nvpn_ip: 1\r\n", "hub_url: h\r\nvpn_ip: 1\r\n", true},
		{"comments and blank lines stay", "# set by the installer\nhub_url: h\n\nnode_id: x\n# end\n", "# set by the installer\nhub_url: h\n\n# end\n", true},
		{"no node_id: file untouched", "hub_url: h\nvpn_ip: 1\n", "hub_url: h\nvpn_ip: 1\n", false},
		// Only the top-level key goes: a nested key of that name, a longer key
		// that merely starts the same, and a value that mentions it all stay.
		{"nested key stays", "dashboard:\n  node_id: keep\n", "dashboard:\n  node_id: keep\n", false},
		{"similar key stays", "node_id_hint: keep\nmy_node_id: keep\n", "node_id_hint: keep\nmy_node_id: keep\n", false},
		{"mentioned in a value stays", "work_dir: /tmp/node_id: x\n", "work_dir: /tmp/node_id: x\n", false},
		{"empty file", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := write(t, tc.in, 0o600)
			removed, err := RemoveNodeID(path)
			if err != nil {
				t.Fatal(err)
			}
			if removed != tc.removed {
				t.Errorf("removed = %v, want %v", removed, tc.removed)
			}
			if got := read(t, path); got != tc.want {
				t.Errorf("file =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestRemoveNodeIDKeepsPermissionsAndLeavesNoTempFile(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o644} {
		path := write(t, installerConfig, mode)
		if _, err := RemoveNodeID(path); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("mode = %v, want %v (the installer makes the config private)", info.Mode().Perm(), mode)
		}
		if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
			t.Error("a temporary file was left behind")
		}
	}
}

func TestRemoveNodeIDIsRepeatableAndIgnoresAMissingFile(t *testing.T) {
	path := write(t, installerConfig, 0o600)
	if removed, _ := RemoveNodeID(path); !removed {
		t.Fatal("first call should remove the line")
	}
	once := read(t, path)
	if removed, err := RemoveNodeID(path); err != nil || removed {
		t.Errorf("second call: removed=%v err=%v, want false/nil", removed, err)
	}
	if read(t, path) != once {
		t.Error("a second call changed the file")
	}
	if removed, err := RemoveNodeID(filepath.Join(t.TempDir(), "nope.conf")); err != nil || removed {
		t.Errorf("missing file: removed=%v err=%v, want false/nil", removed, err)
	}
}

// A config with the old line, and one already migrated, both load, count as
// enrolled and keep their other settings (the Hub can be updated before an
// agent has cleaned its config, and the reverse).
func TestConfigLoadsWithOrWithoutTheOldNodeID(t *testing.T) {
	migrated := `hub_url: http://10.101.0.1:8080
vpn_ip: 10.101.0.4
enrolled: true
dashboard:
  port: 8081
`
	for name, content := range map[string]string{"old config": installerConfig, "migrated config": migrated} {
		cfg, err := Load(write(t, content, 0o600))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !cfg.IsEnrolled() {
			t.Errorf("%s: should count as enrolled", name)
		}
		if cfg.VPNIP != "10.101.0.4" || cfg.Dashboard.Port != 8081 {
			t.Errorf("%s: other settings lost: %+v", name, cfg)
		}
	}
}

func TestIsEnrolledNeedsHubAddressAndFlag(t *testing.T) {
	ok := Config{HubURL: "http://h", VPNIP: "10.0.0.2", Enrolled: true}
	if !ok.IsEnrolled() {
		t.Error("a node with a hub, an address and the flag is enrolled without any ID")
	}
	for name, c := range map[string]Config{
		"not flagged": {HubURL: "http://h", VPNIP: "10.0.0.2"},
		"no hub":      {VPNIP: "10.0.0.2", Enrolled: true},
		"no address":  {HubURL: "http://h", Enrolled: true},
	} {
		if c.IsEnrolled() {
			t.Errorf("%s should not count as enrolled", name)
		}
	}
}

// Saving a config never writes a node_id.
func TestSaveWritesNoNodeID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.conf")
	c := &Config{HubURL: "http://h", VPNIP: "10.0.0.2", Enrolled: true}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(read(t, path), "\n") {
		if strings.HasPrefix(line, "node_id") {
			t.Errorf("saved config has a node_id line: %q", line)
		}
	}
}
