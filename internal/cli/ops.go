package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// installed is one agent installed on this machine (one per Hub).
type installed struct {
	slug   string // e.g. hub-example-com
	unit   string // systemd unit or launchd label
	config string // its agent.conf
}

func configRoot() string {
	if runtime.GOOS == "darwin" {
		return "/usr/local/etc/asdl"
	}
	return "/etc/asdl"
}

// findInstalled finds the agent to act on: the only one, or the one --hub names.
func (e *env) findInstalled() (*installed, error) {
	dirs, _ := filepath.Glob(filepath.Join(configRoot(), "*"))
	var all []installed
	for _, d := range dirs {
		fi, err := os.Stat(d)
		if err != nil || !fi.IsDir() {
			continue
		}
		slug := filepath.Base(d)
		in := installed{slug: slug, config: filepath.Join(d, "agent.conf"), unit: "asdl-agent-" + slug}
		if runtime.GOOS == "darwin" {
			in.unit = "website.asdl.agent." + slug
		}
		all = append(all, in)
	}
	if e.hub != "" {
		for _, in := range all {
			if in.slug == e.hub {
				return &in, nil
			}
		}
		return nil, fmt.Errorf("no agent for %q in %s", e.hub, configRoot())
	}
	switch len(all) {
	case 0:
		return nil, fmt.Errorf("no agent is installed here (nothing in %s)", configRoot())
	case 1:
		return &all[0], nil
	}
	names := make([]string, len(all))
	for i, in := range all {
		names[i] = in.slug
	}
	return nil, fmt.Errorf("this machine has agents for several Hubs (%s); pick one with --hub", strings.Join(names, ", "))
}

func needRoot(what string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("%s needs root: run it with sudo", what)
	}
	return nil
}

func runLive(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// service status|restart|logs [-f] [-n lines]
func (e *env) service(args []string) error {
	in, err := e.findInstalled()
	if err != nil {
		return err
	}
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	mac := runtime.GOOS == "darwin"
	switch sub {
	case "status":
		if mac {
			return runLive("launchctl", "print", "system/"+in.unit)
		}
		_ = runLive("systemctl", "status", in.unit, "--no-pager", "--lines=0")
		return nil
	case "restart":
		if err := needRoot("restarting the agent"); err != nil {
			return err
		}
		return e.restartAgent(in)
	case "logs":
		n, follow := "50", false
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "-f", "--follow":
				follow = true
			case "-n":
				if i+1 < len(args) {
					n = args[i+1]
					i++
				}
			}
		}
		if mac {
			return runLive("log", "show", "--last", "1h", "--predicate", "process == \"asdl-agent-"+in.slug+"\"")
		}
		a := []string{"-u", in.unit, "-n", n, "--no-pager", "-o", "cat"}
		if follow {
			a = append(a, "-f")
		}
		return runLive("journalctl", a...)
	}
	return usageError{"expected asdl-agent service status|restart|logs [-f] [-n lines]"}
}

func (e *env) restartAgent(in *installed) error {
	fmt.Fprintln(e.out, "Restarting the agent…")
	var out []byte
	var err error
	if runtime.GOOS == "darwin" {
		out, err = exec.Command("launchctl", "kickstart", "-k", "system/"+in.unit).CombinedOutput()
	} else {
		out, err = exec.Command("systemctl", "restart", in.unit).CombinedOutput()
	}
	if err != nil {
		return fmt.Errorf("restart failed: %s", strings.TrimSpace(string(out)))
	}
	for i := 0; i < 20; i++ {
		time.Sleep(time.Second)
		if a, err := e.findAgent(); err == nil && a.status["node_id"] != nil {
			fmt.Fprintln(e.out, e.paint(green, "The agent is back up."))
			return nil
		}
	}
	return fmt.Errorf("the agent didn't answer within 20 seconds: asdl-agent service logs")
}

// Settings in agent.conf.
var agentSettings = map[string]struct{ desc, risk string }{
	"interval":       {desc: "how often it sends a heartbeat, e.g. 30s"},
	"max_jobs":       {desc: "jobs it runs at once"},
	"work_dir":       {desc: "folder for job files"},
	"dashboard.port": {desc: "port of the local dashboard (and this command line's connection)"},
	"hub_url":        {desc: "the Hub's address on the mesh", risk: "the agent stops reaching the Hub if it's wrong"},
	"vpn_ip":         {desc: "this node's mesh address", risk: "it must match the WireGuard interface, or the Hub won't recognise this node"},
	"node_id":        {desc: "this node's ID at the Hub", risk: "the Hub would treat this machine as a different node"},
	"enrolled":       {desc: "whether enrollment finished", risk: "false makes the agent enroll again on its next start"},
}

// mapping finds (or with create, adds) the value node for a dotted key.
func mapping(root *yaml.Node, key string, create bool) *yaml.Node {
	n := root
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			if !create {
				return nil
			}
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
		}
		n = n.Content[0]
	}
	parts := strings.Split(key, ".")
	for i, part := range parts {
		var next *yaml.Node
		for j := 0; j+1 < len(n.Content); j += 2 {
			if n.Content[j].Value == part {
				next = n.Content[j+1]
				break
			}
		}
		if next == nil {
			if !create {
				return nil
			}
			kind := yaml.ScalarNode
			if i < len(parts)-1 {
				kind = yaml.MappingNode
			}
			next = &yaml.Node{Kind: kind}
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: part}, next)
		}
		n = next
	}
	return n
}

func removeKey(root *yaml.Node, key string) bool {
	parts := strings.Split(key, ".")
	parent := root
	if len(parts) > 1 {
		parent = mapping(root, strings.Join(parts[:len(parts)-1], "."), false)
	} else if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		parent = root.Content[0]
	}
	if parent == nil {
		return false
	}
	last := parts[len(parts)-1]
	for j := 0; j+1 < len(parent.Content); j += 2 {
		if parent.Content[j].Value == last {
			parent.Content = append(parent.Content[:j], parent.Content[j+2:]...)
			return true
		}
	}
	return false
}

func flatten(prefix string, n *yaml.Node, out *[][2]string) {
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for j := 0; j+1 < len(n.Content); j += 2 {
		k := n.Content[j].Value
		if prefix != "" {
			k = prefix + "." + k
		}
		if v := n.Content[j+1]; v.Kind == yaml.MappingNode {
			flatten(k, v, out)
		} else {
			*out = append(*out, [2]string{k, v.Value})
		}
	}
}

// config [list] | get KEY | set KEY=VALUE... | unset KEY... [--restart] [--force]
func (e *env) config(args []string) error {
	flags := map[string]bool{}
	var rest []string
	for _, a := range args {
		if a == "--restart" || a == "--no-restart" || a == "--force" {
			flags[a] = true
		} else {
			rest = append(rest, a)
		}
	}
	in, err := e.findInstalled()
	if err != nil {
		return err
	}
	if err := needRoot("the agent's config"); err != nil {
		return err
	}
	data, err := os.ReadFile(in.config)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%s: %v", in.config, err)
	}
	sub := "list"
	if len(rest) > 0 {
		sub, rest = rest[0], rest[1:]
	}
	switch sub {
	case "list":
		var kv [][2]string
		flatten("", &doc, &kv)
		w := e.table()
		fmt.Fprintln(w, "SETTING\tVALUE\tWHAT IT IS")
		for _, p := range kv {
			fmt.Fprintf(w, "%s\t%s\t%s\n", p[0], p[1], agentSettings[p[0]].desc)
		}
		w.Flush()
		fmt.Fprintf(e.out, "\nIn %s.\n", in.config)
		return nil
	case "get":
		if len(rest) != 1 {
			return usageError{"expected asdl-agent config get KEY"}
		}
		n := mapping(&doc, rest[0], false)
		if n == nil {
			return fmt.Errorf("%s isn't set", rest[0])
		}
		fmt.Fprintln(e.out, n.Value)
		return nil
	case "set", "unset":
		if len(rest) == 0 {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return usageError{"expected asdl-agent config " + sub + " KEY..."}
			}
			// Ask what to change instead of taking it on the command line.
			in := bufio.NewReader(os.Stdin)
			q := "Which setting do you want to change? (Enter when done) "
			if sub == "unset" {
				q = "Which setting do you want to reset to its default? (Enter when done) "
			}
			for {
				fmt.Fprint(e.out, q)
				k, _ := in.ReadString('\n')
				if k = strings.TrimSpace(k); k == "" {
					break
				}
				if _, ok := agentSettings[k]; !ok {
					fmt.Fprintf(e.out, "  unknown setting %q\n", k)
					continue
				}
				if sub == "unset" {
					rest = append(rest, k)
					continue
				}
				cur := ""
				if n := mapping(&doc, k, false); n != nil {
					cur = " (now " + n.Value + ")"
				}
				fmt.Fprintf(e.out, "New value for %s%s: ", k, cur)
				v, _ := in.ReadString('\n')
				if v = strings.TrimSpace(v); v != "" {
					rest = append(rest, k+"="+v)
				}
			}
			if len(rest) == 0 {
				fmt.Fprintln(e.out, "Nothing changed.")
				return nil
			}
		}
		for _, a := range rest {
			k, v, ok := strings.Cut(a, "=")
			if sub == "set" && !ok {
				return usageError{fmt.Sprintf("%q: use KEY=VALUE", a)}
			}
			s, known := agentSettings[k]
			if !known {
				return usageError{fmt.Sprintf("unknown setting %q (see asdl-agent config)", k)}
			}
			if s.risk != "" && !flags["--force"] {
				return fmt.Errorf("changing %s is risky: %s. Add --force if you're sure", k, s.risk)
			}
			if sub == "set" {
				n := mapping(&doc, k, true)
				n.Kind, n.Value, n.Tag, n.Style = yaml.ScalarNode, v, "", 0
				fmt.Fprintf(e.out, "Set %s=%s.\n", k, v)
			} else if removeKey(&doc, k) {
				fmt.Fprintf(e.out, "Removed %s (the default applies).\n", k)
			}
		}
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(&doc); err != nil {
			return err
		}
		out := buf.Bytes()
		fi, _ := os.Stat(in.config)
		_ = os.WriteFile(in.config+".bak", data, fi.Mode().Perm())
		if err := os.WriteFile(in.config, out, fi.Mode().Perm()); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "Saved %s (the previous version is in agent.conf.bak).\n", in.config)
		restart := flags["--restart"]
		if !restart && !flags["--no-restart"] && term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprint(e.out, "Restart the agent now to use it? [Y/n] ")
			var a string
			fmt.Fscanln(os.Stdin, &a)
			a = strings.ToLower(strings.TrimSpace(a))
			restart = a == "" || a == "y" || a == "yes"
		}
		if !restart {
			fmt.Fprintln(e.out, "The agent uses it after a restart: sudo asdl-agent service restart")
			return nil
		}
		return e.restartAgent(in)
	}
	return usageError{"expected asdl-agent config [list|get|set|unset]"}
}
