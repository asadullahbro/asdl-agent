package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asdl/agent/internal/client"
	"github.com/asdl/agent/pkg/models"
)

func TestJobSlots_CeilingAndDrain(t *testing.T) {
	s := &jobSlots{}
	for i := 0; i < maxParallelJobs; i++ {
		if !s.tryStart() {
			t.Fatalf("slot %d refused below the ceiling", i+1)
		}
	}
	if s.tryStart() {
		t.Fatal("a slot was given beyond the ceiling")
	}
	for i := 0; i < maxParallelJobs; i++ {
		s.done()
	}

	// Draining: nothing new starts, and drain returns once the running job finishes.
	defer func(d time.Duration) { drainPoll = d }(drainPoll)
	drainPoll = 10 * time.Millisecond
	s.tryStart()
	go func() { time.Sleep(50 * time.Millisecond); s.done() }()
	s.drain(0, time.Second)
	if s.active != 0 {
		t.Fatalf("drain returned with %d job(s) running", s.active)
	}
	if s.tryStart() {
		t.Fatal("a job started while draining")
	}
	s.resume()
	if !s.tryStart() {
		t.Fatal("no job may start after resume")
	}
}

// The Hub restarting (connection refused / 502 for a moment) must not lose a job's result.
func TestReportResult_RetriesUntilTheHubIsBack(t *testing.T) {
	defer func(d time.Duration) { reportFirstWait = d }(reportFirstWait)
	reportFirstWait = 10 * time.Millisecond

	var calls atomic.Int32
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer hub.Close()

	err := reportResult(context.Background(), client.NewClient(hub.URL, "10.0.0.2"), &models.JobResult{JobID: "j1", Status: "completed"})
	if err != nil || calls.Load() != 3 {
		t.Fatalf("err %v after %d call(s), want success on the 3rd", err, calls.Load())
	}
}

func TestReportResult_StopsOnRefusal(t *testing.T) {
	defer func(d time.Duration) { reportFirstWait = d }(reportFirstWait)
	reportFirstWait = 10 * time.Millisecond

	var calls atomic.Int32
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer hub.Close()

	if err := reportResult(context.Background(), client.NewClient(hub.URL, "10.0.0.2"), &models.JobResult{JobID: "gone"}); err == nil {
		t.Fatal("a 404 was treated as delivered")
	}
	if calls.Load() != 1 {
		t.Errorf("retried a refusal %d times", calls.Load())
	}
}

func TestGoingAway_TellsTheHub(t *testing.T) {
	var path string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.Method + " " + r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer hub.Close()
	if err := client.NewClient(hub.URL, "10.0.0.2").GoingAway(time.Second); err != nil {
		t.Fatal(err)
	}
	if path != "POST /api/v1/nodes/self/going-away" {
		t.Errorf("sent %q", path)
	}

	// An older Hub without the endpoint: an error to log, nothing more.
	old := httptest.NewServer(http.NotFoundHandler())
	defer old.Close()
	if err := client.NewClient(old.URL, "10.0.0.2").GoingAway(time.Second); err == nil {
		t.Error("a 404 was taken as delivered")
	}
}
