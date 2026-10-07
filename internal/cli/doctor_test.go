package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDoctor_FindsProblems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"hostname": "box", "version": "v1", "hub_url": "http://127.0.0.1:1",
			"disk_total": 100 << 30, "disk_used": 97 << 30, "memory_total": 8 << 30, "memory_used": 2 << 30,
			"update": map[string]any{"current": "v1", "latest": "v2", "update_available": true, "auto_update": true},
			"connection": map[string]any{
				"last_heartbeat_ok": time.Now().Add(-10 * time.Minute).Format(time.RFC3339Nano), "consecutive_failures": 20.0,
				"last_error": "dial tcp 10.0.0.1:8080: i/o timeout",
			},
			"containers": []any{
				map[string]any{"name": "api", "state": "exited", "status": "Exited (1) 2 minutes ago", "managed": true},
				map[string]any{"name": "mine", "state": "exited", "managed": false},
			},
		})
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	old := candidatePorts
	candidatePorts = []int{port}
	defer func() { candidatePorts = old }()

	var out bytes.Buffer
	e := &env{out: &out, version: "v1"}
	if err := e.doctor(); err != errSilent {
		t.Fatalf("doctor returned %v, want problems", err)
	}
	got := out.String()
	for _, want := range []string{"v2 is available", "Heartbeats are failing", "i/o timeout", "Can't reach the Hub", "api is exited", "Disk almost full", "3 problems"} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "mine is") {
		t.Error("containers the Hub didn't start shouldn't be reported")
	}
}

func TestDoctor_NoAgent(t *testing.T) {
	old := candidatePorts
	candidatePorts = []int{1}
	defer func() { candidatePorts = old }()
	var out bytes.Buffer
	if err := (&env{out: &out, hub: "none-such"}).doctor(); err != errSilent {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(out.String(), "no ASDL Agent is answering") {
		t.Errorf("report:\n%s", out.String())
	}
}
