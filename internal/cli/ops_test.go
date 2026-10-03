package cli

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfigEditKeepsOtherKeys(t *testing.T) {
	src := "hub_url: http://10.101.0.1:8080\nvpn_ip: 10.101.0.3\ninterval: 30s\ndashboard:\n  port: 8081\n"
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	n := mapping(&doc, "interval", true)
	n.Value = "15s"
	p := mapping(&doc, "dashboard.port", true)
	p.Value = "8085"
	m := mapping(&doc, "max_jobs", true)
	m.Kind, m.Value = yaml.ScalarNode, "3"
	if !removeKey(&doc, "vpn_ip") {
		t.Fatal("vpn_ip not removed")
	}
	out, _ := yaml.Marshal(&doc)
	got := string(out)
	for _, want := range []string{"hub_url: http://10.101.0.1:8080", "interval: 15s", "port: 8085", "max_jobs: 3"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "vpn_ip") {
		t.Errorf("vpn_ip still there:\n%s", got)
	}
	var kv [][2]string
	flatten("", &doc, &kv)
	if len(kv) != 4 || kv[2][0] != "dashboard.port" {
		t.Errorf("flatten: %v", kv)
	}
}
