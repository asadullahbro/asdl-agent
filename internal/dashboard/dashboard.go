package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/asdl/agent/internal/monitor"
	"github.com/asdl/agent/internal/nodestate"
	"github.com/asdl/agent/internal/updates"
	"github.com/asdl/agent/pkg/models"
)

// Actions are things the node's dashboard can ask the agent to do.
type Actions struct {
	CheckForUpdate func()
	InstallUpdate  func()
	SetMaintenance func(on bool) (*models.MaintenanceResult, error)
}

//go:embed index.html
var htmlFile embed.FS

type JobEntry struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	EndedAt   time.Time `json:"ended_at"`
	ExitCode  int       `json:"exit_code"`
}

type RingBuffer struct {
	mu      sync.RWMutex
	entries []JobEntry
	max     int
}

func NewRingBuffer(max int) *RingBuffer {
	return &RingBuffer{max: max, entries: []JobEntry{}}
}

func (r *RingBuffer) Push(e JobEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append([]JobEntry{e}, r.entries...)
	if len(r.entries) > r.max {
		r.entries = r.entries[:r.max]
	}
}

func (r *RingBuffer) Get() []JobEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]JobEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

type Dashboard struct {
	mon     *monitor.Monitor
	jobs    *RingBuffer
	updates *updates.State
	conn    *nodestate.Connection
	wgShake func() (time.Time, error)
	actions Actions
	hubURL  string
	nodeID  string
	vpnIP   string
	version string
	port    int
}

func New(mon *monitor.Monitor, jobs *RingBuffer, upd *updates.State, hubURL, nodeID, vpnIP, version string, port int) *Dashboard {
	return &Dashboard{
		mon:     mon,
		jobs:    jobs,
		updates: upd,
		hubURL:  hubURL,
		nodeID:  nodeID,
		vpnIP:   vpnIP,
		version: version,
		port:    port,
	}
}

// SetNodeState gives the dashboard the connection tracker and a way to read
// the WireGuard handshake.
func (d *Dashboard) SetNodeState(conn *nodestate.Connection, wgHandshake func() (time.Time, error)) {
	d.conn, d.wgShake = conn, wgHandshake
}

func (d *Dashboard) SetActions(a Actions) { d.actions = a }

func (d *Dashboard) Start() {
	mux := http.NewServeMux()
	d.routes(mux)

	addr := fmt.Sprintf(":%d", d.port)
	log.Printf("Dashboard listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("Dashboard failed to start: %v", err)
	}
}

func (d *Dashboard) routes(mux *http.ServeMux) {

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := htmlFile.ReadFile("index.html")
		if err != nil {
			http.Error(w, "dashboard not found", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
	})

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		hb, err := d.mon.GetHeartbeat()
		if err != nil {
			http.Error(w, `{"error":"monitor unavailable"}`, 500)
			return
		}

		info, _ := d.mon.GetSystemInfo()

		json.NewEncoder(w).Encode(map[string]any{
			"node_id":      d.nodeID,
			"hostname":     info.Hostname,
			"vpn_ip":       d.vpnIP,
			"hub_url":      d.hubURL,
			"version":      d.version,
			"uptime":       hb.Uptime,
			"cpu_percent":  hb.CPUPercent,
			"memory_used":  hb.MemoryUsed,
			"memory_total": hb.MemoryTotal,
			"disk_used":    hb.DiskUsed,
			"disk_total":   hb.DiskTotal,
			"load_avg_1":   hb.LoadAvg1,
			"load_avg_5":   hb.LoadAvg5,
			"load_avg_15":  hb.LoadAvg15,
			"ping_latency": hb.PingLatency,
			"jobs":         d.jobs.Get(),
			"update":       d.updates.Snapshot(),
			"containers":   d.containers(r),
			"connection":   d.connection(),
			"time":         time.Now().Unix(),
		})
	})

	// Turning automatic updates on or off is only allowed from this machine.
	// The custom header makes browsers preflight the request, which this
	// server doesn't allow, so other websites can't flip it either.
	mux.HandleFunc("/api/auto-update", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"use POST"}`, http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("X-ASDL-Agent") != "1" {
			http.Error(w, `{"error":"missing X-ASDL-Agent header"}`, http.StatusBadRequest)
			return
		}
		if !fromLoopback(r) {
			http.Error(w, `{"error":"change this setting from the node itself (http://localhost)"}`, http.StatusForbidden)
			return
		}
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
			http.Error(w, `{"error":"send {\"enabled\": true|false}"}`, http.StatusBadRequest)
			return
		}
		if err := d.updates.SetAutoUpdate(*req.Enabled); err != nil {
			log.Printf("⚠️ Could not save the auto-update setting: %v", err)
			http.Error(w, `{"error":"could not save the setting"}`, http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(d.updates.Snapshot())
	})

	mux.HandleFunc("POST /api/updates/check", d.localOnly(func(w http.ResponseWriter, r *http.Request) {
		if d.actions.CheckForUpdate == nil {
			http.Error(w, `{"error":"not available"}`, http.StatusServiceUnavailable)
			return
		}
		d.actions.CheckForUpdate()
		json.NewEncoder(w).Encode(map[string]string{"status": "checking"})
	}))

	// Installs the latest release now, whatever the auto-update setting.
	mux.HandleFunc("POST /api/updates/install", d.localOnly(func(w http.ResponseWriter, r *http.Request) {
		if d.actions.InstallUpdate == nil || !d.updates.Available() {
			http.Error(w, `{"error":"no update available"}`, http.StatusConflict)
			return
		}
		d.actions.InstallUpdate()
		json.NewEncoder(w).Encode(map[string]string{"status": "installing"})
	}))

	mux.HandleFunc("POST /api/maintenance", d.localOnly(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
			http.Error(w, `{"error":"send {\"enabled\": true|false}"}`, http.StatusBadRequest)
			return
		}
		if d.actions.SetMaintenance == nil {
			http.Error(w, `{"error":"not available"}`, http.StatusServiceUnavailable)
			return
		}
		res, err := d.actions.SetMaintenance(*req.Enabled)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(res)
	}))

	// Logs can contain secrets, so they're only served on the node itself.
	mux.HandleFunc("GET /api/containers/{name}/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !fromLoopback(r) {
			http.Error(w, `{"error":"logs are only shown on the node itself (http://localhost)"}`, http.StatusForbidden)
			return
		}
		lines, err := strconv.Atoi(r.URL.Query().Get("lines"))
		if err != nil || lines < 1 || lines > 2000 {
			lines = 200
		}
		out, err := nodestate.Logs(r.Context(), r.PathValue("name"), lines)
		resp := map[string]string{"logs": out}
		if err != nil {
			resp["error"] = err.Error()
		}
		json.NewEncoder(w).Encode(resp)
	})
}

func fromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// localOnly wraps actions that change something: they must come from this
// machine and carry the X-ASDL-Agent header (so browsers preflight them and
// other websites can't trigger them).
func (d *Dashboard) localOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-ASDL-Agent") != "1" {
			http.Error(w, `{"error":"missing X-ASDL-Agent header"}`, http.StatusBadRequest)
			return
		}
		if !fromLoopback(r) {
			http.Error(w, `{"error":"do this from the node itself (http://localhost)"}`, http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (d *Dashboard) containers(r *http.Request) any {
	list, err := nodestate.Containers(r.Context())
	if err != nil {
		return map[string]string{"error": err.Error()}
	}
	return list
}

func (d *Dashboard) connection() map[string]any {
	snap := map[string]any{}
	if d.conn != nil {
		snap = d.conn.Snapshot()
	}
	if d.wgShake != nil {
		if t, err := d.wgShake(); err != nil {
			snap["wg_error"] = err.Error()
		} else {
			snap["wg_handshake"] = t
		}
	}
	return snap
}
