package dashboard

import (
	"embed"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/asdl/agent/internal/monitor"
)


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
	hubURL  string
	nodeID  string
	vpnIP   string
	version string
}

func New(mon *monitor.Monitor, jobs *RingBuffer, hubURL, nodeID, vpnIP, version string) *Dashboard {
	return &Dashboard{
		mon:     mon,
		jobs:    jobs,
		hubURL:  hubURL,
		nodeID:  nodeID,
		vpnIP:   vpnIP,
		version: version,
	}
}

func (d *Dashboard) Start() {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, _ := htmlFile.ReadFile("index.html")
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
			"time":         time.Now().Unix(),
		})
	})

	http.ListenAndServe(":8081", mux)
}