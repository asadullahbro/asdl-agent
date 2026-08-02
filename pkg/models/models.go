package models

import "time"

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

