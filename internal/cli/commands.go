package cli

import (
	"fmt"
	"net/url"
	"time"
)

func parseTime(v any) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, str(v))
	return t
}

func ago(t time.Time) string {
	if t.IsZero() || t.Year() < 2000 {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func gb(v any) string { return fmt.Sprintf("%.1f", num(v)/(1<<30)) }

func (e *env) status(a *agent) error {
	s := a.status
	if e.json {
		return e.printJSON(s)
	}
	conn, _ := s["connection"].(map[string]any)
	upd, _ := s["update"].(map[string]any)

	hub := e.paint(green, "connected")
	lastOK := parseTime(conn["last_heartbeat_ok"])
	if fails := num(conn["consecutive_failures"]); fails > 0 || lastOK.IsZero() || time.Since(lastOK) > 2*time.Minute {
		hub = e.paint(red, "not reaching the Hub")
		if msg := str(conn["last_error"]); msg != "" {
			hub += " (" + msg + ")"
		}
	}
	maint := "off"
	if on, _ := conn["maintenance"].(bool); on {
		maint = e.paint(yellow, "on: the Hub puts no apps here")
	} else if known, _ := conn["maintenance_known"].(bool); !known {
		maint = "unknown until the next heartbeat"
	}
	update := "up to date"
	if avail, _ := upd["update_available"].(bool); avail {
		update = e.paint(yellow, str(upd["latest"])+" available: asdl-agent update install")
	}
	if u := str(upd["updating"]); u != "" {
		update = "installing " + u
	}
	auto := "off"
	if on, _ := upd["auto_update"].(bool); on {
		auto = "on"
	}
	running, total := 0, 0
	if list, ok := s["containers"].([]any); ok {
		for _, c := range list {
			m, _ := c.(map[string]any)
			total++
			if str(m["state"]) == "running" {
				running++
			}
		}
	}
	wg := "-"
	if t := parseTime(conn["wg_handshake"]); !t.IsZero() {
		wg = "handshake " + ago(t)
	}

	w := e.table()
	fmt.Fprintf(w, "Node\t%s (%s)\n", str(s["hostname"]), str(s["vpn_ip"]))
	fmt.Fprintf(w, "Hub\t%s, %s, last heartbeat %s\n", str(s["hub_url"]), hub, ago(lastOK))
	fmt.Fprintf(w, "Mesh\t%s\n", wg)
	fmt.Fprintf(w, "Maintenance\t%s\n", maint)
	fmt.Fprintf(w, "Agent\t%s, %s (auto-update %s)\n", str(s["version"]), update, auto)
	fmt.Fprintf(w, "Apps\t%d running of %d containers\n", running, total)
	fmt.Fprintf(w, "CPU\t%.0f%%, load %.2f %.2f %.2f\n", num(s["cpu_percent"]), num(s["load_avg_1"]), num(s["load_avg_5"]), num(s["load_avg_15"]))
	fmt.Fprintf(w, "Memory\t%s / %s GB\n", gb(s["memory_used"]), gb(s["memory_total"]))
	fmt.Fprintf(w, "Disk\t%s / %s GB\n", gb(s["disk_used"]), gb(s["disk_total"]))
	fmt.Fprintf(w, "Up\t%s\n", (time.Duration(num(s["uptime"])) * time.Second).String())
	return w.Flush()
}

func (e *env) apps(a *agent) error {
	list, ok := a.status["containers"].([]any)
	if e.json {
		return e.printJSON(a.status["containers"])
	}
	if !ok {
		if m, isErr := a.status["containers"].(map[string]any); isErr {
			return fmt.Errorf("%s", str(m["error"]))
		}
		fmt.Fprintln(e.out, "No containers.")
		return nil
	}
	w := e.table()
	fmt.Fprintln(w, "CONTAINER\tFROM\tSTATE\tSTATUS\tIMAGE")
	for _, c := range list {
		m, _ := c.(map[string]any)
		from := "-"
		if managed, _ := m["managed"].(bool); managed {
			from = "Hub"
		}
		state := str(m["state"])
		switch state {
		case "running":
			state = e.paint(green, state)
		case "restarting", "paused":
			state = e.paint(yellow, state)
		default:
			state = e.paint(red, state)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", str(m["name"]), from, state, str(m["status"]), str(m["image"]))
	}
	return w.Flush()
}

func (e *env) jobs(a *agent) error {
	list, _ := a.status["jobs"].([]any)
	if e.json {
		return e.printJSON(list)
	}
	if len(list) == 0 {
		fmt.Fprintln(e.out, "No jobs since the agent started.")
		return nil
	}
	w := e.table()
	fmt.Fprintln(w, "JOB\tTYPE\tSTATUS\tSTARTED\tTOOK")
	for i := len(list) - 1; i >= 0; i-- {
		m, _ := list[i].(map[string]any)
		start, end := parseTime(m["created_at"]), parseTime(m["ended_at"])
		took := "-"
		if !end.IsZero() && end.Year() > 2000 {
			took = end.Sub(start).Round(100 * time.Millisecond).String()
		}
		id := str(m["id"])
		if len(id) > 8 {
			id = id[:8]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", id, str(m["type"]), str(m["status"]), ago(start), took)
	}
	return w.Flush()
}

func (e *env) logs(a *agent, name string, lines int) error {
	var res struct {
		Logs  string `json:"logs"`
		Error string `json:"error"`
	}
	if err := a.get(fmt.Sprintf("/api/containers/%s/logs?lines=%d", url.PathEscape(name), lines), &res); err != nil {
		return err
	}
	fmt.Fprint(e.out, res.Logs)
	if res.Error != "" {
		return fmt.Errorf("%s", res.Error)
	}
	return nil
}

func (e *env) restart(a *agent, name string) error {
	fmt.Fprintf(e.out, "Restarting %s…\n", name)
	if err := a.post("/api/containers/"+url.PathEscape(name)+"/restart", nil, nil); err != nil {
		return err
	}
	fmt.Fprintln(e.out, e.paint(green, "Restarted."))
	return nil
}

func (e *env) maintenance(a *agent, on bool) error {
	var res struct {
		Moving []string `json:"moving"`
		Stays  []string `json:"stays"`
	}
	if err := a.post("/api/maintenance", map[string]bool{"enabled": on}, &res); err != nil {
		return err
	}
	if e.json {
		return e.printJSON(res)
	}
	host := str(a.status["hostname"])
	if !on {
		fmt.Fprintf(e.out, "%s is out of maintenance and takes apps again.\n", host)
		return nil
	}
	fmt.Fprintf(e.out, "%s is in maintenance: the Hub puts no new apps here.\n", host)
	for _, m := range res.Moving {
		fmt.Fprintf(e.out, "  moving  %s\n", m)
	}
	for _, s := range res.Stays {
		fmt.Fprintf(e.out, "  %s    %s (no other node can take it)\n", e.paint(yellow, "stays"), s)
	}
	return nil
}

func (e *env) refresh(a *agent) map[string]any {
	var s map[string]any
	if a.get("/api/status", &s) == nil {
		a.status = s
	}
	upd, _ := a.status["update"].(map[string]any)
	return upd
}

func (e *env) updateCheck(a *agent) error {
	before := str(e.refresh(a)["checked_at"])
	if err := a.post("/api/updates/check", nil, nil); err != nil {
		return err
	}
	var upd map[string]any
	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		if upd = e.refresh(a); str(upd["checked_at"]) != before {
			break
		}
	}
	if e.json {
		return e.printJSON(upd)
	}
	switch {
	case str(upd["check_error"]) != "":
		fmt.Fprintf(e.out, "Running %s; couldn't check for updates: %s\n", str(upd["current"]), str(upd["check_error"]))
	case upd["update_available"] == true:
		fmt.Fprintf(e.out, "%s is available (running %s). Install it with `asdl-agent update install`.\n", str(upd["latest"]), str(upd["current"]))
	default:
		fmt.Fprintf(e.out, "Running %s, the latest release.\n", str(upd["current"]))
	}
	return nil
}

func (e *env) updateInstall(a *agent) error {
	upd := e.refresh(a)
	if upd["update_available"] != true {
		// The agent may not have looked yet: check now, then install.
		before := str(upd["checked_at"])
		if err := a.post("/api/updates/check", nil, nil); err != nil {
			return err
		}
		for i := 0; i < 20 && str(upd["checked_at"]) == before; i++ {
			time.Sleep(500 * time.Millisecond)
			upd = e.refresh(a)
		}
		if upd["update_available"] != true {
			if msg := str(upd["check_error"]); msg != "" {
				return fmt.Errorf("couldn't check for updates: %s", msg)
			}
			fmt.Fprintf(e.out, "Running %s, the latest release.\n", str(upd["current"]))
			return nil
		}
	}
	target := str(upd["latest"])
	if err := a.post("/api/updates/install", nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(e.out, "Installing %s (the agent restarts)…\n", target)
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		var s map[string]any
		if a.get("/api/status", &s) == nil && str(s["version"]) == target {
			fmt.Fprintln(e.out, e.paint(green, "Now running "+target+"."))
			return nil
		}
		if u, _ := s["update"].(map[string]any); u != nil && str(u["last_error"]) != "" && str(u["updating"]) == "" {
			return fmt.Errorf("the update failed: %s", str(u["last_error"]))
		}
	}
	return fmt.Errorf("the agent isn't on %s after 3 minutes; check journalctl -u 'asdl-agent*'", target)
}

func (e *env) autoUpdate(a *agent, on bool) error {
	if err := a.post("/api/auto-update", map[string]bool{"enabled": on}, nil); err != nil {
		return err
	}
	if on {
		fmt.Fprintln(e.out, "Automatic updates are on: new agent releases install by themselves.")
	} else {
		fmt.Fprintln(e.out, "Automatic updates are off. Update with `asdl-agent update install`.")
	}
	return nil
}
