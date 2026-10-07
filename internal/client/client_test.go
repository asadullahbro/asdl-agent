package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/asdl/agent/pkg/models"
)

// A stand-in Hub that answers like the real one and keeps what it was sent.
type fakeHub struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen []string // "METHOD /path?query body"
}

// The ID the Hub gives the node. The agent must never send it back.
const hubAssignedID = "9d3b6f0e-1111-4222-8333-444455556666"

func newFakeHub(t *testing.T) *fakeHub {
	t.Helper()
	h := &fakeHub{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.seen = append(h.seen, r.Method+" "+r.URL.RequestURI()+" "+string(body))
		h.mu.Unlock()

		switch {
		case r.URL.Path == "/api/v1/nodes":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(models.NodeInfo{ID: hubAssignedID, Hostname: "box"})
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			json.NewEncoder(w).Encode(models.HeartbeatReply{})
		case strings.HasSuffix(r.URL.Path, "/maintenance"):
			json.NewEncoder(w).Encode(models.MaintenanceResult{})
		case r.URL.Path == "/api/v1/jobs/claim":
			json.NewEncoder(w).Encode(models.Job{ID: "job-1"})
		case strings.HasSuffix(r.URL.Path, "/complete"):
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *fakeHub) requests() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.seen...)
}

func TestRequestsCarryNoNodeID(t *testing.T) {
	hub := newFakeHub(t)
	c := NewClient(hub.srv.URL, "10.101.0.4")

	if err := c.Register(&models.NodeInfo{Hostname: "box"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendHeartbeat(&models.Heartbeat{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetMaintenance(true); err != nil {
		t.Fatal(err)
	}
	job, err := c.ClaimJob()
	if err != nil || job == nil || job.ID != "job-1" {
		t.Fatalf("claim: %v %+v", err, job)
	}
	if err := c.CompleteJob(&models.JobResult{JobID: "job-1", Status: "completed"}); err != nil {
		t.Fatal(err)
	}

	got := hub.requests()
	want := []string{
		"POST /api/v1/nodes ",
		"POST /api/v1/nodes/self/heartbeat ",
		"POST /api/v1/nodes/self/maintenance ",
		"POST /api/v1/jobs/claim ",
		"POST /api/v1/jobs/job-1/complete ",
	}
	if len(got) != len(want) {
		t.Fatalf("requests = %q", got)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("request %d = %q, want it to start with %q", i, got[i], want[i])
		}
	}
	for _, req := range got {
		if strings.Contains(req, hubAssignedID) || strings.Contains(req, "node_id") {
			t.Errorf("the agent sent a node ID: %q", req)
		}
	}
}

func TestNothingIsSentBeforeRegistering(t *testing.T) {
	hub := newFakeHub(t)
	c := NewClient(hub.srv.URL, "10.101.0.4")

	if _, err := c.SendHeartbeat(&models.Heartbeat{}); err == nil {
		t.Error("heartbeat before registering should fail")
	}
	if _, err := c.SetMaintenance(true); err == nil {
		t.Error("maintenance before registering should fail")
	}
	if _, err := c.ClaimJob(); err == nil {
		t.Error("claiming before registering should fail")
	}
	if got := hub.requests(); len(got) != 0 {
		t.Errorf("requests sent before registering: %q", got)
	}
}

func TestFailedRegistrationDoesNotCountAsRegistered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"nope"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "10.101.0.4")

	if err := c.Register(&models.NodeInfo{}); err == nil {
		t.Fatal("registration should fail on a 500")
	}
	if _, err := c.SendHeartbeat(&models.Heartbeat{}); err == nil {
		t.Error("a failed registration must not allow heartbeats")
	}
}
