package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// A doctor check's outcome.
const (
	levelOK = iota
	levelInfo
	levelWarn
	levelFail
)

type finding struct {
	Section string   `json:"section"`
	Level   string   `json:"level"`
	Message string   `json:"message"`
	Fix     []string `json:"fix,omitempty"`
}

type report struct {
	e        *env
	section  string
	counts   [4]int
	findings []finding
}

var levelNames = [4]string{"ok", "info", "warning", "problem"}

func (r *report) start(section string) {
	r.section = section
	if !r.e.json {
		fmt.Fprintf(r.e.out, "\n%s\n", r.e.paint("1", section))
	}
}

func (r *report) add(level int, msg string, fix ...string) {
	r.counts[level]++
	r.findings = append(r.findings, finding{r.section, levelNames[level], msg, fix})
	if r.e.json {
		return
	}
	mark := [4]string{r.e.paint(green, "✓"), r.e.paint(grey, "·"), r.e.paint(yellow, "!"), r.e.paint(red, "✗")}[level]
	fmt.Fprintf(r.e.out, "  %s %s\n", mark, msg)
	for _, f := range fix {
		fmt.Fprintf(r.e.out, "      %s %s\n", r.e.paint(grey, "→"), f)
	}
}

func (r *report) ok(format string, a ...any) { r.add(levelOK, fmt.Sprintf(format, a...)) }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// errSilent makes the command exit 1 without printing anything more.
var errSilent = errors.New("")

func (r *report) finish() error {
	if r.e.json {
		if err := r.e.printJSON(r.findings); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(r.e.out, "\n%s, %s, %s\n", plural(r.counts[levelOK], "ok", "ok"),
			plural(r.counts[levelWarn], "warning", "warnings"), plural(r.counts[levelFail], "problem", "problems"))
	}
	if r.counts[levelFail] > 0 {
		return errSilent
	}
	return nil
}

// doctor checks this node and its agent, and says what to fix.
func (e *env) doctor() error {
	r := &report{e: e}
	if !e.json {
		fmt.Fprintln(e.out, "ASDL Agent doctor")
	}

	r.start("Agent")
	a, err := e.findAgent()
	if err != nil {
		r.add(levelFail, err.Error(), serviceHints()...)
		return r.finish()
	}
	s := a.status
	r.ok("The agent answers (%s on %s)", str(s["version"]), str(s["hostname"]))
	if e.version != str(s["version"]) && e.version != "dev" {
		r.add(levelInfo, fmt.Sprintf("This command is %s; the running agent is %s (they match again after the next restart)", e.version, str(s["version"])))
	}
	upd, _ := s["update"].(map[string]any)
	switch {
	case str(upd["last_error"]) != "":
		r.add(levelWarn, "The last update failed: "+str(upd["last_error"]), "asdl-agent update install")
	case upd["update_available"] == true:
		r.add(levelWarn, fmt.Sprintf("%s is available (running %s)", str(upd["latest"]), str(upd["current"])), "asdl-agent update install")
	case str(upd["check_error"]) != "":
		r.add(levelWarn, "Can't check for updates: "+str(upd["check_error"]), "This machine needs to reach api.github.com")
	default:
		r.ok("Up to date")
	}
	if upd["auto_update"] != true {
		r.add(levelInfo, "Automatic updates are off (asdl-agent auto-update on)")
	}

	r.start("Hub connection")
	conn, _ := s["connection"].(map[string]any)
	hubURL := str(s["hub_url"])
	lastOK := parseTime(conn["last_heartbeat_ok"])
	switch fails := num(conn["consecutive_failures"]); {
	case lastOK.IsZero() || lastOK.Year() < 2000:
		r.add(levelFail, "No heartbeat has reached the Hub since the agent started: "+orNone(str(conn["last_error"])), meshHints(hubURL)...)
	case fails > 0 || time.Since(lastOK) > 2*time.Minute:
		r.add(levelFail, fmt.Sprintf("Heartbeats are failing (last one that worked %s): %s", ago(lastOK), orNone(str(conn["last_error"]))), meshHints(hubURL)...)
	default:
		r.ok("Heartbeats reach the Hub (last %s)", ago(lastOK))
	}
	if t := parseTime(conn["wg_handshake"]); !t.IsZero() && t.Year() > 2000 {
		if time.Since(t) > 3*time.Minute {
			r.add(levelFail, "WireGuard: last handshake with the Hub "+ago(t), meshHints(hubURL)...)
		} else {
			r.ok("WireGuard tunnel is up (handshake %s)", ago(t))
		}
	} else if msg := str(conn["wg_error"]); msg != "" {
		r.add(levelWarn, "Can't read the WireGuard state: "+msg, "sudo wg show")
	}
	e.checkAddress(r, str(s["vpn_ip"]))
	e.checkHub(r, hubURL)
	if on, _ := conn["maintenance"].(bool); on {
		r.add(levelWarn, "This node is in maintenance: the Hub puts no apps here", "When you're done: asdl-agent maintenance off")
	}

	r.start("Apps")
	switch list := s["containers"].(type) {
	case map[string]any:
		r.add(levelFail, "Docker isn't working: "+str(list["error"]), "sudo systemctl status docker; sudo systemctl start docker")
	case []any:
		managed, down := 0, 0
		for _, c := range list {
			m, _ := c.(map[string]any)
			if hub, _ := m["managed"].(bool); !hub {
				continue
			}
			managed++
			if state := str(m["state"]); state != "running" {
				down++
				r.add(levelWarn, fmt.Sprintf("%s is %s (%s)", str(m["name"]), state, str(m["status"])),
					"asdl-agent logs "+str(m["name"]), "asdl-agent restart "+str(m["name"]))
			}
		}
		if down == 0 {
			r.ok("Docker works; %s from the Hub running", plural(managed, "app", "apps"))
		}
	default:
		r.ok("Docker works; no containers")
	}

	r.start("Resources")
	if total := num(s["disk_total"]); total > 0 {
		free := (total - num(s["disk_used"])) / total * 100
		freeGB := (total - num(s["disk_used"])) / (1 << 30)
		switch {
		case free < 5:
			r.add(levelFail, fmt.Sprintf("Disk almost full: %.1f GB (%.0f%%) free; deploys will fail", freeGB, free), "docker system prune -a (removes unused images)")
		case free < 10:
			r.add(levelWarn, fmt.Sprintf("Disk getting full: %.1f GB (%.0f%%) free", freeGB, free), "docker system prune (removes unused images)")
		default:
			r.ok("Disk: %.1f GB (%.0f%%) free", freeGB, free)
		}
	}
	if total := num(s["memory_total"]); total > 0 {
		used := num(s["memory_used"]) / total * 100
		if used > 92 {
			r.add(levelWarn, fmt.Sprintf("Memory %.0f%% used", used), "See what uses it: docker stats --no-stream")
		} else {
			r.ok("Memory: %.0f%% used", used)
		}
	}
	if load, cores := num(s["load_avg_5"]), float64(runtime.NumCPU()); load > cores*2 {
		r.add(levelWarn, fmt.Sprintf("Load %.1f on %d cores: the machine is overloaded", load, int(cores)))
	}
	return r.finish()
}

func orNone(s string) string {
	if s == "" {
		return "no error recorded"
	}
	return s
}

// checkHub calls the Hub's /health from this machine and compares clocks.
func (e *env) checkHub(r *report, hubURL string) {
	if hubURL == "" {
		return
	}
	cl := &http.Client{Timeout: 5 * time.Second}
	resp, err := cl.Get(strings.TrimRight(hubURL, "/") + "/health")
	if err != nil {
		r.add(levelFail, "Can't reach the Hub at "+hubURL+": "+err.Error(), meshHints(hubURL)...)
		return
	}
	defer resp.Body.Close()
	var h struct {
		Time string `json:"time"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&h)
	r.ok("The Hub answers at %s", hubURL)
	if t, err := time.Parse(time.RFC3339, h.Time); err == nil {
		if skew := time.Since(t); skew > time.Minute || skew < -time.Minute {
			r.add(levelWarn, fmt.Sprintf("This machine's clock is %s off from the Hub's", skew.Round(time.Second)), "Turn on time sync: sudo timedatectl set-ntp true")
		}
	}
}

// localAddrs lists the addresses on this machine's network interfaces.
var localAddrs = func() ([]net.Addr, error) { return net.InterfaceAddrs() }

// checkAddress compares the VPN address in the agent's config with the ones
// this machine really has. The Hub knows a node by the address its traffic
// comes from, so a config that names another address (or a WireGuard
// interface that's gone) means the agent can't be recognised.
func (e *env) checkAddress(r *report, want string) {
	if want == "" {
		r.add(levelFail, "The agent's config has no vpn_ip, so it counts as not enrolled", "Add this machine as a node again: https://docs.asdl.website/hub/add-a-node/")
		return
	}
	addrs, err := localAddrs()
	if err != nil {
		r.add(levelWarn, "Can't read this machine's network addresses: "+err.Error())
		return
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.String() == want {
			r.ok("This machine has the VPN address in its config (%s)", want)
			return
		}
	}
	r.add(levelFail, "The config says this node is "+want+", but no network interface here has that address",
		"sudo wg show (is the asdl-* interface up, and is that its address?)",
		"The Hub decides which address a node has: if it was changed there, add this machine as a node again")
}

func meshHints(hubURL string) []string {
	return []string{
		"sudo wg show (is there a recent handshake?)",
		"Is the Hub's WireGuard UDP port open in its cloud firewall?",
		"journalctl -u 'asdl-agent*' -n 50",
	}
}

// serviceHints says how to check the agent's service on this OS.
func serviceHints() []string {
	if runtime.GOOS == "darwin" {
		return []string{"launchctl list | grep asdl", "Start it: launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.asdl.agent.plist"}
	}
	out, _ := exec.Command("systemctl", "list-units", "--all", "--no-legend", "--plain", "asdl-agent*").Output()
	var units []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f := strings.Fields(l); len(f) >= 4 {
			units = append(units, f[0]+" is "+f[2]+"/"+f[3])
		}
	}
	if len(units) == 0 {
		return []string{"No asdl-agent service on this machine: add it as a node (https://docs.asdl.website/hub/add-a-node/)"}
	}
	hints := []string{strings.Join(units, "; ")}
	return append(hints, "sudo systemctl restart 'asdl-agent*'", "journalctl -u 'asdl-agent*' -n 50")
}
