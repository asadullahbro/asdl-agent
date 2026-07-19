# ASDL Agent

A lightweight node agent for [ASDL Hub](https://github.com/asadullahbro/asdl-hub) — the distributed container management system. Runs on each node in the WireGuard mesh, polls the hub for jobs, and executes them locally.

## What it does

- Registers itself with the hub on startup
- Sends periodic heartbeats (CPU, memory, disk, ping latency)
- Polls for and executes jobs dispatched by the hub
- Handles container deployments, migrations, and failovers
- Transfers Docker images over WireGuard via SSH when available, falls back to GitHub build

## Installation

### Download binary

```bash
# Linux
curl -fsSL https://github.com/asadullahbro/asdl-agent/releases/latest/download/asdl-agent-linux -o /usr/local/bin/asdl-agent
chmod +x /usr/local/bin/asdl-agent

# macOS
curl -fsSL https://github.com/asadullahbro/asdl-agent/releases/latest/download/asdl-agent-mac -o /usr/local/bin/asdl-agent
chmod +x /usr/local/bin/asdl-agent
```

### Configure

Create `/etc/asdl/config.yaml` (or `config.yaml` in the working directory):

```yaml
hub_url: "http://10.100.0.1:8080"
vpn_ip: "10.100.0.x"       # this node's WireGuard IP
interval: 30s
work_dir: "/tmp/asdl"
max_jobs: 5
```

### Run as a service

**Linux (systemd)**

```ini
# /etc/systemd/system/asdl-agent.service
[Unit]
Description=ASDL Agent
After=network.target

[Service]
ExecStart=/usr/local/bin/asdl-agent --config /etc/asdl/config.yaml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable asdl-agent
sudo systemctl start asdl-agent
```

**macOS (launchctl)**

```xml
<!-- ~/Library/LaunchAgents/com.asdl.agent.plist -->
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.asdl.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/asdl-agent</string>
        <string>--config</string>
        <string>/etc/asdl/config.yaml</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
</dict>
</plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.asdl.agent.plist
```

## SSH key setup

For image transfers between nodes over WireGuard:

```bash
sudo mkdir -p /etc/asdl
sudo ssh-keygen -t ed25519 -f /etc/asdl/asdl_transfer -N "" -C "asdl-hub"
echo 'from="10.100.0.0/24" '$(cat /etc/asdl/asdl_transfer.pub) >> ~/.ssh/authorized_keys
```

Run this on every node. The key is restricted to the WireGuard subnet — no external access.

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