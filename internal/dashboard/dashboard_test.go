package dashboard

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asdl/agent/internal/updates"
)

func TestAutoUpdateToggleOnlyFromThisMachine(t *testing.T) {
	upd := updates.Load(filepath.Join(t.TempDir(), "state.json"), "v1")
	d := New(nil, NewRingBuffer(1), upd, "", "", "v1", 0)
	mux := http.NewServeMux()
	d.routes(mux)

	post := func(remote, header string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/auto-update", strings.NewReader(`{"enabled":false}`))
		req.RemoteAddr = remote
		if header != "" {
			req.Header.Set("X-ASDL-Agent", header)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w.Code
	}

	if code := post("192.168.1.20:5000", "1"); code != http.StatusForbidden {
		t.Errorf("from the LAN: %d, want 403", code)
	}
	if code := post("127.0.0.1:5000", ""); code != http.StatusBadRequest {
		t.Errorf("without the header: %d, want 400", code)
	}
	if !upd.AutoUpdate() {
		t.Fatal("rejected requests must not change the setting")
	}
	if code := post("[::1]:5000", "1"); code != http.StatusOK || upd.AutoUpdate() {
		t.Errorf("from localhost: %d, auto-update %v; want 200 and off", code, upd.AutoUpdate())
	}
}
