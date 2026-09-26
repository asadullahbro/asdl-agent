package updates

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAutoUpdatePreferencePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-state.json")
	s := Load(path, "v2026.09.26-aaaaaaa")
	if !s.AutoUpdate() {
		t.Fatal("auto-update should default to on")
	}
	if err := s.SetAutoUpdate(false); err != nil {
		t.Fatal(err)
	}
	if Load(path, "v2026.09.26-aaaaaaa").AutoUpdate() {
		t.Fatal("turning auto-update off must survive a restart")
	}
}

func TestAvailable(t *testing.T) {
	s := Load(filepath.Join(t.TempDir(), "x.json"), "v2026.09.26-aaaaaaa")
	if s.Available() {
		t.Error("nothing checked yet")
	}
	s.Checked("v2026.09.27-bbbbbbb", nil)
	if !s.Available() {
		t.Error("a different release is out")
	}
	s.Checked("", errors.New("offline"))
	if !s.Available() || s.Snapshot()["check_error"] != "offline" {
		t.Error("a failed check keeps the last known release and reports the error")
	}
	d := Load(filepath.Join(t.TempDir(), "y.json"), "dev")
	d.Checked("v2026.09.27-bbbbbbb", nil)
	if d.Available() {
		t.Error("development builds never update themselves")
	}
}
