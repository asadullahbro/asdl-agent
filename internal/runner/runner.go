package runner

import (
    "bytes"
    "context"
    "fmt"
    "io"
    "os"
    "os/exec"
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
    // Create work directory if it doesn't exist
    if err := os.MkdirAll(workDir, 0755); err != nil {
        // Log but continue
    }

    return &Runner{
        workDir: workDir,
        jobs:    make(map[string]*runningJob),
    }
}

func (r *Runner) Execute(ctx context.Context, job *models.Job) (*models.JobResult, error) {
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
    logBuffer.WriteString(fmt.Sprintf("Command: %s\n", job.Command))
    logBuffer.WriteString(fmt.Sprintf("Working Dir: %s\n", job.WorkingDir))
    logBuffer.WriteString("====================================\n\n")

    // Prepare command
    cmd := exec.CommandContext(ctx, "sh", "-c", job.Command)
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