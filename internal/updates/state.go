// Package updates tracks the agent's own version, the latest release, and
// whether the agent may update itself, for the self-updater and the local
// dashboard.
package updates

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Sources of an update in progress.
const (
	SourceAuto = "auto" // the agent's own periodic check
	SourceHub  = "hub"  // an agent_update job sent by the Hub
)

type State struct {
	path string

	mu         sync.RWMutex
	current    string
	latest     string
	checkedAt  time.Time
	checkError string
	autoUpdate bool
	updating   string // target version, or "latest" for Hub jobs
	source     string
	lastError  string
}

type saved struct {
	AutoUpdate bool `json:"auto_update"`
}

// Load reads the saved preference from path (auto-update is on when the
// file doesn't exist yet).
func Load(path, current string) *State {
	s := &State{path: path, current: current, autoUpdate: true}
	if data, err := os.ReadFile(path); err == nil {
		var v saved
		if json.Unmarshal(data, &v) == nil {
			s.autoUpdate = v.AutoUpdate
		}
	}
	return s
}

// StatePath is where the preference is kept: next to the agent's config.
func StatePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "agent-state.json")
}

func (s *State) AutoUpdate() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.autoUpdate
}

func (s *State) SetAutoUpdate(on bool) error {
	s.mu.Lock()
	s.autoUpdate = on
	s.mu.Unlock()
	data, _ := json.Marshal(saved{AutoUpdate: on})
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return err
	}
	log.Printf("Automatic updates turned %s", map[bool]string{true: "on", false: "off"}[on])
	return nil
}

func (s *State) Checked(latest string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkedAt = time.Now()
	if err != nil {
		s.checkError = err.Error()
		return
	}
	s.latest, s.checkError = latest, ""
}

func (s *State) Started(target, source string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updating, s.source, s.lastError = target, source, ""
}

// Failed records an update that didn't complete; the agent keeps running
// its current version.
func (s *State) Failed(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updating, s.source, s.lastError = "", "", msg
}

// Available reports whether a release other than the running one is out.
// Development builds never update themselves.
func (s *State) Available() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.available()
}

func (s *State) available() bool {
	return s.latest != "" && s.latest != s.current && s.current != "dev"
}

// Snapshot is what the dashboard shows.
func (s *State) Snapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{
		"current":          s.current,
		"latest":           s.latest,
		"update_available": s.available(),
		"checked_at":       s.checkedAt,
		"check_error":      s.checkError,
		"auto_update":      s.autoUpdate,
		"updating":         s.updating,
		"source":           s.source,
		"last_error":       s.lastError,
	}
}
