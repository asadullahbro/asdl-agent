// Package cli is the asdl-agent command line. It talks to the agent running
// on this machine through its local dashboard API, so it only works on the
// node itself.
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

const usage = `ASDL Agent %s

Usage: asdl-agent <command> [arguments]

  status                   This node: Hub connection, maintenance, updates, resources
  doctor                   Check this node and say what to fix
  apps                     Containers on this node
  logs <app> [-n lines]    A container's recent logs
  restart <app>            Restart a container
  jobs                     Jobs the Hub sent recently
  maintenance on|off       Move apps off this node before working on it, or end that
  update                   Is a new agent release out?
  update install           Install it now
  auto-update on|off       Install new releases by itself, or not
  service status|restart|logs   The agent's service (restart: sudo); logs -f follows it
  config                   The agent's settings (sudo)
  config set [KEY=VALUE]   Change settings (asks which, if none given), then restart
  version                  This program's version
  run                      Run the agent (what the service runs)

On a machine with agents for more than one Hub, pick one with --hub <name>.
Add --json to print the agent's answer.
Docs: https://docs.asdl.website/hub/agent/cli/
`

type env struct {
	out     io.Writer
	version string
	json    bool
	color   bool
	hub     string
}

// IsCommand reports whether args ask for a CLI command rather than the agent.
// The service runs the agent with flags (-config ...) or none.
func IsCommand(args []string) bool {
	if len(args) == 0 {
		// Services start with no terminal on stdin. (INVOCATION_ID can't
		// tell them apart: desktops start terminals under systemd too.)
		return term.IsTerminal(int(os.Stdin.Fd()))
	}
	return !strings.HasPrefix(args[0], "-") && args[0] != "run"
}

// Run runs one command and returns the exit code.
func Run(args []string, version string) int {
	e := &env{out: os.Stdout, version: version, color: term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == ""}
	var rest []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--json":
			e.json = true
		case a == "--hub" && i+1 < len(args):
			e.hub = args[i+1]
			i++
		case strings.HasPrefix(a, "--hub="):
			e.hub = strings.TrimPrefix(a, "--hub=")
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "-h" || rest[0] == "--help" {
		fmt.Fprintf(e.out, usage, version)
		return 0
	}
	if err := e.run(rest[0], rest[1:]); err != nil {
		if err == errSilent {
			return 1
		}
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintf(os.Stderr, "asdl-agent %s: %s\nRun `asdl-agent help` for the commands.\n", rest[0], ue.msg)
			return 2
		}
		fmt.Fprintf(os.Stderr, "asdl-agent %s: %v\n", rest[0], err)
		return 1
	}
	return 0
}

type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

func (e *env) run(cmd string, args []string) error {
	if cmd == "version" || cmd == "--version" {
		fmt.Fprintln(e.out, "asdl-agent", e.version)
		return nil
	}
	switch cmd {
	case "doctor":
		return e.doctor()
	case "service":
		return e.service(args)
	case "config":
		return e.config(args)
	}
	a, err := e.findAgent()
	if err != nil {
		return err
	}
	switch cmd {
	case "status":
		return e.status(a)
	case "apps", "containers":
		return e.apps(a)
	case "jobs":
		return e.jobs(a)
	case "logs":
		fs := flag.NewFlagSet("logs", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		n := fs.Int("n", 100, "lines")
		var pos []string
		for len(args) > 0 {
			if err := fs.Parse(args); err != nil {
				return usageError{err.Error()}
			}
			if args = fs.Args(); len(args) > 0 {
				pos, args = append(pos, args[0]), args[1:]
			}
		}
		if len(pos) == 0 {
			return usageError{"expected a container name"}
		}
		return e.logs(a, pos[0], *n)
	case "restart":
		if len(args) == 0 {
			return usageError{"expected a container name"}
		}
		return e.restart(a, args[0])
	case "maintenance":
		if len(args) == 0 || (args[0] != "on" && args[0] != "off") {
			return usageError{"expected asdl-agent maintenance on|off"}
		}
		return e.maintenance(a, args[0] == "on")
	case "update":
		if len(args) > 0 && args[0] == "install" {
			return e.updateInstall(a)
		}
		return e.updateCheck(a)
	case "auto-update", "autoupdate":
		if len(args) == 0 || (args[0] != "on" && args[0] != "off") {
			return usageError{"expected asdl-agent auto-update on|off"}
		}
		return e.autoUpdate(a, args[0] == "on")
	}
	return usageError{fmt.Sprintf("unknown command %q", cmd)}
}

// agent is a running agent's local API.
type agent struct {
	base   string
	status map[string]any
	http   *http.Client
}

// Agents keep their config in /etc/asdl/<hub>/agent.conf (root only). Its
// dashboard port is read from there when possible; otherwise the ports the
// installer picks from are tried.
var candidatePorts = []int{8081, 8082, 8083, 8084, 8085, 8086, 8087, 8088, 8089, 8090, 8099, 5000}

func (e *env) findAgent() (*agent, error) {
	hc := &http.Client{Timeout: 5 * time.Second}
	ports := configPorts(e.hub)
	if len(ports) == 0 {
		ports = candidatePorts
	}
	var found []*agent
	for _, p := range ports {
		a := &agent{base: fmt.Sprintf("http://127.0.0.1:%d", p), http: hc}
		if err := a.get("/api/status", &a.status); err != nil {
			continue
		}
		// Anything answering /api/status that reports a version is an agent
		if _, ok := a.status["version"]; !ok {
			continue
		}
		if e.hub != "" && !strings.Contains(str(a.status["hub_url"]), e.hub) && len(configPorts(e.hub)) == 0 {
			continue
		}
		found = append(found, a)
	}
	switch len(found) {
	case 0:
		return nil, errors.New("no ASDL Agent is answering on this machine (is it running? systemctl status 'asdl-agent*')")
	case 1:
		return found[0], nil
	}
	var hubs []string
	for _, a := range found {
		hubs = append(hubs, str(a.status["hub_url"]))
	}
	return nil, fmt.Errorf("this machine has agents for several Hubs (%s); pick one with --hub", strings.Join(hubs, ", "))
}

// configPorts returns dashboard ports from the agent configs this user can
// read, only those of hub when it is given.
func configPorts(hub string) []int {
	paths, _ := filepath.Glob("/etc/asdl/*/agent.conf")
	var ports []int
	for _, p := range paths {
		if hub != "" && filepath.Base(filepath.Dir(p)) != hub {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var cfg struct {
			Dashboard struct {
				Port int `yaml:"port"`
			} `yaml:"dashboard"`
		}
		if yaml.Unmarshal(b, &cfg) == nil && cfg.Dashboard.Port > 0 {
			ports = append(ports, cfg.Dashboard.Port)
		}
	}
	return ports
}

func (a *agent) get(path string, out any) error {
	resp, err := a.http.Get(a.base + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decode(resp, out)
}

// post sends a change; the agent only takes those from this machine with
// the X-ASDL-Agent header.
func (a *agent) post(path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(http.MethodPost, a.base+path, rd)
	req.Header.Set("X-ASDL-Agent", "1")
	req.Header.Set("Content-Type", "application/json")
	c := *a.http
	c.Timeout = 90 * time.Second
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decode(resp, out)
}

func decode(resp *http.Response, out any) error {
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(data))
		}
		return errors.New(e.Error)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

func (e *env) table() *tabwriter.Writer { return tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0) }

const (
	green  = "32"
	red    = "31"
	yellow = "33"
	grey   = "90"
)

func (e *env) paint(code, s string) string {
	if !e.color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (e *env) printJSON(v any) error {
	enc := json.NewEncoder(e.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
