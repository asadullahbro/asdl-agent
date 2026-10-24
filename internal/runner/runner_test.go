//go:build !windows

package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"strings"
	"testing"
	"time"

	"github.com/asdl/agent/pkg/models"
)

// Cancelling a job must stop what its shell started, not only the shell: a
// `docker logs -f` is a child of sh, and a job that never ends blocks every
// job queued behind it.
func TestCancelStopsTheWholeJob(t *testing.T) {
	r := NewRunner(t.TempDir())
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan *models.JobResult, 1)
	go func() {
		cmd := "sleep 300 & echo $! > " + pidFile + "; wait"
		res, err := r.Execute(ctx, &models.Job{ID: "j1", Type: "command", Command: cmd}, nil)
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	var pid int
	for i := 0; i < 50 && pid == 0; i++ {
		time.Sleep(100 * time.Millisecond)
		if b, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
	}
	if pid == 0 {
		t.Fatal("the job's child never started")
	}
	cancel(errors.New("the Hub says this job is over"))

	select {
	case res := <-done:
		if res.Status != "failed" || !strings.Contains(res.Logs, "the Hub says this job is over") {
			t.Fatalf("result: %s / %s", res.Status, res.Logs)
		}
	case <-time.After(5 * time.Second):
		syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("Execute did not return after the job was cancelled")
	}
	time.Sleep(200 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err == nil {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("the child process %d survived the cancel", pid)
	}
}
