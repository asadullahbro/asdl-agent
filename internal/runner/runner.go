package runner

import (
    "bytes"
    "context"
    "fmt"
    "io"
    "os"
    "os/exec"
    "strings"
    "sync"
    "time"

    "github.com/asdl/agent/pkg/models"
)

type Runner struct {
    workDir string
    jobs    map[string]*runningJob
    mu      sync.RWMutex
}

type runningJob struct {
    cmd     *exec.Cmd
    cancel  context.CancelFunc
    started time.Time
    logs    *bytes.Buffer
}

func NewRunner(workDir string) *Runner {
    if err := os.MkdirAll(workDir, 0755); err != nil {
        // Log but continue
    }

    return &Runner{
        workDir: workDir,
        jobs:    make(map[string]*runningJob),
    }
}

// BuildCommands generates shell script lines if job.Command is empty but Payload exists
func (r *Runner) BuildCommands(job *models.Job) string {
    if strings.TrimSpace(job.Command) != "" {
        return job.Command
    }

    if job.Payload == nil {
        return ""
    }

    p := job.Payload
    var cmds []string

    switch job.Type {
    case "migrate_start", "deploy":
        // 1. Pull or Clone
        if p.Repository != "" {
            cmds = append(cmds, fmt.Sprintf(
                "echo \"🔨 Building from repo: %s\"\ncd /tmp\nrm -rf %s\ngit clone %s %s\ncd %s\ngit checkout main || git checkout master || true\ndocker build -t %s .",
                p.Repository, p.ContainerName, p.Repository, p.ContainerName, p.ContainerName, p.Image,
            ))
        } else if p.Image != "" {
            cmds = append(cmds, fmt.Sprintf("echo \"📥 Pulling image: %s\"\ndocker pull %s", p.Image, p.Image))
        }

        // 2. Stop and remove existing container
        cmds = append(cmds, fmt.Sprintf("echo \"🛑 Cleaning old container...\"\ndocker stop %s 2>/dev/null || true\ndocker rm %s 2>/dev/null || true", p.ContainerName, p.ContainerName))

        // 3. Build docker run
        var runParts []string
        runParts = append(runParts, fmt.Sprintf("docker run -d --name %s --restart unless-stopped", p.ContainerName))

        for _, port := range p.Ports {
            if port != "" {
                runParts = append(runParts, fmt.Sprintf("-p %s", port))
            }
        }
        for _, vol := range p.Volumes {
            if vol != "" {
                runParts = append(runParts, fmt.Sprintf("-v %s", vol))
            }
        }
        for _, env := range p.EnvVars {
            if env.Key != "" {
                runParts = append(runParts, fmt.Sprintf("-e %s=%s", env.Key, env.Value))
            }
        }
        runParts = append(runParts, p.Image)

        cmds = append(cmds, fmt.Sprintf("echo \"🚀 Starting container: %s\"\n%s", p.ContainerName, strings.Join(runParts, " ")))

    case "failover_start":
        cmds = append(cmds, fmt.Sprintf("docker stop %s 2>/dev/null || true\ndocker rm %s 2>/dev/null || true", p.ContainerName, p.ContainerName))
        if p.SourceNodeIP != "" && p.Image != "" {
            cmds = append(cmds, fmt.Sprintf("ssh -o StrictHostKeyChecking=no %s \"docker save %s\" | docker load || docker pull %s", p.SourceNodeIP, p.Image, p.Image))
        } else if p.Image != "" {
            cmds = append(cmds, fmt.Sprintf("docker pull %s", p.Image))
        }
        
        var runParts []string
        runParts = append(runParts, fmt.Sprintf("docker run -d --name %s --restart unless-stopped", p.ContainerName))
        for _, port := range p.Ports {
            if port != "" { runParts = append(runParts, fmt.Sprintf("-p %s", port)) }
        }
        for _, vol := range p.Volumes {
            if vol != "" { runParts = append(runParts, fmt.Sprintf("-v %s", vol)) }
        }
        for _, env := range p.EnvVars {
            if env.Key != "" { runParts = append(runParts, fmt.Sprintf("-e %s=%s", env.Key, env.Value)) }
        }
        runParts = append(runParts, p.Image)
        cmds = append(cmds, strings.Join(runParts, " "))
    }

    return strings.Join(cmds, "\n\n")
}

func (r *Runner) Execute(ctx context.Context, job *models.Job) (*models.JobResult, error) {
    // Resolve full command string (uses payload if command is empty)
    execCmdStr := r.BuildCommands(job)

    // Create job-specific working directory
    jobDir := fmt.Sprintf("%s/%s", r.workDir, job.ID)
    if err := os.MkdirAll(jobDir, 0755); err != nil {
        return nil, fmt.Errorf("failed to create work dir: %w", err)
    }

    // Ensure cleanup
    defer os.RemoveAll(jobDir)

    // Create log buffer
    var logBuffer bytes.Buffer
    logBuffer.WriteString(fmt.Sprintf("=== Job %s started at %s ===\n", job.ID, time.Now().Format(time.RFC3339)))
    logBuffer.WriteString(fmt.Sprintf("Type: %s\n", job.Type))
    logBuffer.WriteString(fmt.Sprintf("Command:\n%s\n", execCmdStr))
    logBuffer.WriteString(fmt.Sprintf("Working Dir: %s\n", job.WorkingDir))
    logBuffer.WriteString("====================================\n\n")

    // If there is still no command to execute, fail gracefully
    if strings.TrimSpace(execCmdStr) == "" {
        logBuffer.WriteString("❌ Error: No command or valid payload provided for job execution\n")
        return &models.JobResult{
            JobID:    job.ID,
            Status:   "failed",
            Logs:     logBuffer.String(),
            ExitCode: 1,
            Duration: 0,
        }, nil
    }

    // Prepare command
    cmd := exec.CommandContext(ctx, "sh", "-c", execCmdStr)
    cmd.Dir = jobDir

    // Set environment variables
    env := os.Environ()
    for _, e := range job.Environment {
        env = append(env, e)
    }
    cmd.Env = env

    // Capture output
    stdout, err := cmd.StdoutPipe()
    if err != nil {
        return nil, err
    }
    stderr, err := cmd.StderrPipe()
    if err != nil {
        return nil, err
    }

    // Start command
    started := time.Now()
    if err := cmd.Start(); err != nil {
        return &models.JobResult{
            JobID:    job.ID,
            Status:   "failed",
            Logs:     fmt.Sprintf("Failed to start: %v", err),
            ExitCode: -1,
            Duration: time.Since(started).Milliseconds(),
        }, nil
    }

    // Store running job
    _, cancel := context.WithCancel(context.Background())
    r.mu.Lock()
    r.jobs[job.ID] = &runningJob{
        cmd:     cmd,
        cancel:  cancel,
        started: started,
        logs:    &logBuffer,
    }
    r.mu.Unlock()
    defer func() {
        r.mu.Lock()
        delete(r.jobs, job.ID)
        r.mu.Unlock()
        cancel()
    }()

    // Read stdout/stderr
    var stdoutBuf, stderrBuf bytes.Buffer
    wg := sync.WaitGroup{}
    wg.Add(2)
    go func() {
        defer wg.Done()
        io.Copy(io.MultiWriter(&logBuffer, &stdoutBuf), stdout)
    }()
    go func() {
        defer wg.Done()
        io.Copy(io.MultiWriter(&logBuffer, &stderrBuf), stderr)
    }()

    // Wait for completion
    err = cmd.Wait()
    wg.Wait()

    duration := time.Since(started).Milliseconds()

    // Determine result
    status := "completed"
    exitCode := 0
    if err != nil {
        status = "failed"
        if exitError, ok := err.(*exec.ExitError); ok {
            exitCode = exitError.ExitCode()
        } else if err != context.Canceled {
            exitCode = -1
        }
    }

    // Add summary
    logBuffer.WriteString(fmt.Sprintf("\n====================================\n"))
    logBuffer.WriteString(fmt.Sprintf("Completed at: %s\n", time.Now().Format(time.RFC3339)))
    logBuffer.WriteString(fmt.Sprintf("Exit Code: %d\n", exitCode))
    logBuffer.WriteString(fmt.Sprintf("Duration: %dms\n", duration))

    if stdoutBuf.Len() > 0 {
        logBuffer.WriteString(fmt.Sprintf("\nSTDOUT:\n%s\n", stdoutBuf.String()))
    }
    if stderrBuf.Len() > 0 {
        logBuffer.WriteString(fmt.Sprintf("\nSTDERR:\n%s\n", stderrBuf.String()))
    }

    return &models.JobResult{
        JobID:    job.ID,
        Status:   status,
        Logs:     logBuffer.String(),
        ExitCode: exitCode,
        Duration: duration,
    }, nil
}

func (r *Runner) GetJobStatus(jobID string) *models.JobResult {
    r.mu.RLock()
    defer r.mu.RUnlock()

    job, exists := r.jobs[jobID]
    if !exists {
        return nil
    }

    return &models.JobResult{
        JobID:    jobID,
        Status:   "running",
        Duration: time.Since(job.started).Milliseconds(),
    }
}

func (r *Runner) KillJob(jobID string) error {
    r.mu.RLock()
    defer r.mu.RUnlock()

    job, exists := r.jobs[jobID]
    if !exists {
        return fmt.Errorf("job %s not found", jobID)
    }

    if job.cancel != nil {
        job.cancel()
    }
    return job.cmd.Process.Kill()
}