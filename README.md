<div align="center">

<img src=".github/assets/logo.svg" alt="ASDL Agent" width="96" height="96">

# ASDL Agent

**The node agent for [ASDL Hub](https://github.com/asadullahbro/ASDL-Hub): it joins the Hub's private network and runs the apps the Hub assigns to this machine.**

[![Latest release](https://img.shields.io/github/v/release/asadullahbro/asdl-agent?color=f29a00&label=release)](https://github.com/asadullahbro/asdl-agent/releases/latest)
[![Docs](https://img.shields.io/badge/docs-docs.asdl.website-f29a00)](https://docs.asdl.website/hub/agent/overview/)

[Documentation](https://docs.asdl.website/hub/agent/overview/) ·
[ASDL Hub](https://github.com/asadullahbro/ASDL-Hub) ·
[Releases](https://github.com/asadullahbro/asdl-agent/releases)

</div>

<br>

Every machine that runs apps for an ASDL Hub — a VPS, a desktop, a laptop —
runs this agent. It only makes outgoing connections to the Hub, so the
machine needs no public IP or open ports.

## What it does

| Feature | What it does |
|---|---|
| **Joins the mesh** | Connects to the Hub over WireGuard and registers the machine as a node. |
| **Reports health** | Sends a heartbeat every 30 seconds with CPU, memory, disk and network latency, which the Hub uses to pick where apps run. |
| **Runs jobs** | Checks for work every 5 seconds: deploying and replacing containers, taking over apps from a failed node, removing stale copies, pulling images. |
| **Keeps secrets out of logs** | Environment values arrive separately from commands and are never written to job logs. |
| **Updates itself** | Checks for a new release every 5 minutes, verifies its checksum and restarts into it. Can be turned off per node (the Hub can still push an update). |
| **Local dashboard** | At `http://localhost:<port>` on the node: its apps and their logs, the connection to the Hub, updates (Check now / Install now / automatic on-off), maintenance mode, resources and recent jobs. |

## Install

Nodes are added from the Hub, not installed by hand. In the Hub dashboard
open **Settings → Node enrollment**, generate a token, and run the command it
shows on the machine:

```bash
curl -fsSL https://<your-hub>/install | sudo bash
```

The script sets up WireGuard, installs the agent as a service and enrolls the
node. Full steps: **[Add a node](https://docs.asdl.website/hub/add-a-node/)**.

Supported: Linux (amd64) and macOS. Install Docker on the machine first.

## Managing the agent

On the node, `asdl-agent` is also a command line for the running agent:

```bash
asdl-agent status                    # Hub connection, maintenance, updates, resources
asdl-agent apps                      # containers on this node
asdl-agent logs <app> -n 100         # a container's recent output
asdl-agent restart <app>
asdl-agent maintenance on|off        # move apps off this node, or end that
asdl-agent update install            # install a new release now
asdl-agent auto-update on|off
```

See the [command line docs](https://docs.asdl.website/hub/agent/cli/). The service itself:

```bash
systemctl status 'asdl-agent-*'      # is it running?
journalctl -u 'asdl-agent-*' -f      # what is it doing?
```

To update every node at once instead of waiting for the next check, use
**Settings → Agent update → Deploy agents** in the Hub.

## Building from source

```bash
git clone https://github.com/asadullahbro/asdl-agent.git
cd asdl-agent
go build -o bin/asdl-agent ./cmd/agent
```

## Releases

Every change merged to `main` is released automatically as
`vYYYY.MM.DD-<commit>`, and nodes pick it up within minutes.
