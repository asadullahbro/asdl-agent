// Package nodestate gathers what the node's dashboard and the Hub want to
// know about this machine beyond raw metrics: its containers, and how the
// connection to the Hub is doing.
package nodestate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asdl/agent/pkg/models"
)

// Containers lists every container on the node, running or not.
func Containers(ctx context.Context) ([]models.Container, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "ps", "-a", "--no-trunc", "--format", "{{json .}}").Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	return parseDockerPS(out), nil
}

func parseDockerPS(out []byte) []models.Container {
	res := []models.Container{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var row struct {
			Names, Image, State, Status, Ports, Labels string
		}
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			continue
		}
		labels := map[string]string{}
		for _, kv := range strings.Split(row.Labels, ",") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				labels[k] = v
			}
		}
		res = append(res, models.Container{
			Name:      row.Names,
			Image:     row.Image,
			State:     row.State,
			Status:    row.Status,
			Ports:     row.Ports,
			Managed:   labels["asdl.managed"] == "true",
			ProjectID: labels["asdl.project"],
		})
	}
	return res
}

var containerNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

// ValidContainerName reports whether name is safe to pass to docker.
func ValidContainerName(name string) bool { return containerNameRe.MatchString(name) }

// Logs returns the last lines of a container's output.
func Logs(ctx context.Context, name string, lines int) (string, error) {
	if !ValidContainerName(name) {
		return "", fmt.Errorf("invalid container name")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "logs", "--tail", strconv.Itoa(lines), "--timestamps", name).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker logs: %w", err)
	}
	return string(out), nil
}

// WireGuardHandshake returns the most recent handshake on the WireGuard
// interface that carries vpnIP (the tunnel to the Hub), or zero if none.
func WireGuardHandshake(ctx context.Context, vpnIP string) (time.Time, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	target := "all"
	// On Linux, find the interface with our address; elsewhere read all.
	if out, err := exec.CommandContext(ctx, "ip", "-o", "-4", "addr", "show").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) >= 4 && strings.HasPrefix(f[3], vpnIP+"/") {
				target = f[1]
				break
			}
		}
	}
	out, err := exec.CommandContext(ctx, "wg", "show", target, "latest-handshakes").Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("wg show: %w", err)
	}
	return latestHandshake(out), nil
}

// latestHandshake reads `wg show <iface|all> latest-handshakes` output: the
// last field of each line is a unix time (0 = never).
func latestHandshake(out []byte) time.Time {
	var latest int64
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if ts, err := strconv.ParseInt(f[len(f)-1], 10, 64); err == nil && ts > latest {
			latest = ts
		}
	}
	if latest == 0 {
		return time.Time{}
	}
	return time.Unix(latest, 0)
}

// Connection remembers how talking to the Hub has gone.
type Connection struct {
	mu            sync.RWMutex
	lastOK        time.Time
	lastErr       string
	lastErrAt     time.Time
	failures      int
	maintenance   bool
	maintKnown    bool
	maintResult   *models.MaintenanceResult
	maintResultAt time.Time
}

func (c *Connection) HeartbeatOK(reply *models.HeartbeatReply) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastOK, c.failures = time.Now(), 0
	if reply != nil {
		c.maintenance, c.maintKnown = reply.Maintenance, true
	}
}

func (c *Connection) HeartbeatFailed(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastErr, c.lastErrAt = err.Error(), time.Now()
	c.failures++
}

// MaintenanceSet records the Hub's answer to a maintenance change made here.
func (c *Connection) MaintenanceSet(on bool, res *models.MaintenanceResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maintenance, c.maintKnown = on, true
	c.maintResult, c.maintResultAt = res, time.Now()
}

func (c *Connection) Snapshot() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snap := map[string]any{
		"last_heartbeat_ok":    c.lastOK,
		"last_error":           c.lastErr,
		"last_error_at":        c.lastErrAt,
		"consecutive_failures": c.failures,
		"maintenance_known":    c.maintKnown,
		"maintenance":          c.maintenance,
	}
	// Show what the last change moved for a few minutes.
	if c.maintResult != nil && time.Since(c.maintResultAt) < 10*time.Minute {
		snap["maintenance_result"] = c.maintResult
	}
	return snap
}
