package models

import (
    "strings"
    "time"
)

type NodeInfo struct {
    ID           string   `json:"id,omitempty"`
    Hostname     string   `json:"hostname"`
    VPNIP        string   `json:"vpn_ip"`
    OS           string   `json:"os"`
    Architecture string   `json:"architecture"`
    CPU          int      `json:"cpu"`
    MemoryTotal  int64    `json:"memory_total"`
    DiskTotal    int64    `json:"disk_total"`
    Capabilities []string `json:"capabilities"`
    Version string `json:"version"`
}

type Heartbeat struct {
    ID           uint      `gorm:"primaryKey" json:"-"`
    NodeID       string    `gorm:"index;not null" json:"node_id"`
    CPUPercent   float64   `json:"cpu_percent"`
    MemoryUsed   int64     `json:"memory_used"`
    MemoryTotal  int64     `json:"memory_total"`
    DiskUsed     int64     `json:"disk_used"`
    DiskTotal    int64     `json:"disk_total"`
    LoadAvg1     float64   `json:"load_avg_1"`
    LoadAvg5     float64   `json:"load_avg_5"`
    LoadAvg15    float64   `json:"load_avg_15"`
    PingLatency  float64   `json:"ping_latency"`
    WiFiSignal   int       `json:"wifi_signal"`
    Uptime       int64     `json:"uptime"`
    Timestamp    time.Time `json:"timestamp"`
    AgentVersion string      `json:"agent_version"`
    Containers   []Container `json:"containers"`
}

// Container is one container on this node, as reported to the Hub.
type Container struct {
    Name      string `json:"name"`
    Image     string `json:"image"`
    State     string `json:"state"`  // running, exited, restarting, ...
    Status    string `json:"status"` // Docker's text, e.g. "Up 3 hours"
    Ports     string `json:"ports"`
    Managed   bool   `json:"managed"` // started by the Hub
    ProjectID string `json:"project_id,omitempty"`
}

// HeartbeatReply is what the Hub answers to a heartbeat.
type HeartbeatReply struct {
    Status      string `json:"status"`
    Maintenance bool   `json:"maintenance"`
}

// MaintenanceResult is the Hub's answer to a maintenance change.
type MaintenanceResult struct {
    Moving []string `json:"moving"`
    Stays  []string `json:"stays"`
    Error  string   `json:"error"`
}

type EnvVar struct {
    Key   string `json:"key"`
    Value string `json:"value"`
}

type JobPayload struct {
    Image         string    `json:"image"`
    ContainerName string    `json:"container_name"`
    SourceNodeIP  string    `json:"source_node_ip"`
    Repository    string    `json:"repository"`
    Branch        string    `json:"branch"`
    BuildCommand  string    `json:"build_command"`
    StartCommand  string    `json:"start_command"`
    InstallCmd    string    `json:"install_cmd"`
    LastDeployed  time.Time `json:"last_deployed"`
    Ports         []string  `json:"ports"`
    Volumes       []string  `json:"volumes"`
    EnvVars       []EnvVar  `json:"env_vars"`
    Operation     string    `json:"operation"`
    MigrationID   string    `json:"migration_id"`
}

type Job struct {
    ID          string    `json:"id"`
    NodeID      string    `json:"node_id"`
    Type        string    `json:"type"`
    Status      string    `json:"status"`
    Command     string    `json:"command"`
    WorkingDir  string    `json:"working_dir"`
    Environment []string  `json:"environment"`
    Timeout     int       `json:"timeout"`
    CreatedAt   time.Time `json:"created_at"`
    Payload *JobPayload `json:"payload,omitempty"`
}

type JobResult struct {
    JobID    string `json:"job_id"`
    Status   string `json:"status"`
    Logs     string `json:"logs"`
    ExitCode int    `json:"exit_code"`
    Duration int64  `json:"duration"`
}


// ImageRef returns image with ":latest" added only when it has no tag or
// digest. The hub stores images from CI with their tag already attached
// ("ghcr.io/o/app:<sha>"); a registry host's port ("host:5000/app") is not a tag.
func ImageRef(image string) string {
    if strings.Contains(image, "@") {
        return image
    }
    lastSlash := strings.LastIndex(image, "/")
    if strings.Contains(image[lastSlash+1:], ":") {
        return image
    }
    return image + ":latest"
}
