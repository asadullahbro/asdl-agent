# ASDL Agent

A lightweight node agent for [ASDL Hub](https://github.com/asadullahbro/asdl-hub) — the distributed container management system. Runs on each node in the WireGuard mesh, polls the hub for jobs, and executes them locally.

## What it does

- Registers itself with the hub on startup
- Sends periodic heartbeats (CPU, memory, disk, ping latency)
- Polls for and executes jobs dispatched by the hub
- Handles container deployments, migrations, and failovers
- Transfers Docker images over WireGuard via SSH when available, falls back to GitHub build
- Self-updates automatically when a new release is available

## Installation

Enrollment is handled entirely by the hub. Run the one-liner from your hub dashboard on any node:

```bash
curl -fsSL https://<your-hub-url>/install | sudo bash
```

This will:
- Download the agent binary
- Configure WireGuard and join the mesh
- Generate SSH keys and register the node with the hub
- Install and start the agent as a system service

No manual configuration needed.

## Building from source

```bash
git clone https://github.com/asadullahbro/asdl-agent.git
cd asdl-agent
go build -o bin/asdl-agent ./cmd/agent
```

## Job types

| Type | Description |
|---|---|
| `deploy` | Clone repo, build image, run container |
| `migrate_start` | Start container on this node after migration |
| `migrate_stop` | Stop and remove container before migration |
| `failover_start` | Start container after automatic health-check failover |
| `agent_update` | Pull latest binary and restart agent |

## Part of ASDL Hub

This agent is designed to work exclusively with [ASDL Hub](https://github.com/asadullahbro/asdl-hub). Nodes communicate over a WireGuard mesh — the hub never exposes node ports to the public internet.
