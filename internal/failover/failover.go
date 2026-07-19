package failover

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/asdl/agent/pkg/models"
)

const (
	sshKey      = "/etc/asdl/asdl_transfer"
	sshTimeout  = "10"
	buildTmpDir = "/tmp/asdl-builds"
)

func Execute(ctx context.Context, job *models.Job) *models.JobResult {
	var logBuf bytes.Buffer
	started := time.Now()

	logBuf.WriteString(fmt.Sprintf("=== %s started at %s ===\n\n", job.Type, started.Format(time.RFC3339)))

	write := func(format string, args ...any) {
		msg := fmt.Sprintf(format+"\n", args...)
		logBuf.WriteString(msg)
		log.Print(msg)
	}

	exit := func(code int) *models.JobResult {
		status := "completed"
		if code != 0 {
			status = "failed"
		}
		return &models.JobResult{
			JobID:    job.ID,
			Status:   status,
			Logs:     logBuf.String(),
			ExitCode: code,
			Duration: time.Since(started).Milliseconds(),
		}
	}

	p := job.Payload
	if p == nil {
		write("❌ No payload found on job")
		return exit(1)
	}

	imageName := fmt.Sprintf("%s:latest", p.Image)

	write("📦 Project: %s", p.ContainerName)
	write("🏷️  Image: %s", imageName)
	write("📡 Type: %s", job.Type)
	write("🖥️  Source node: %s", p.SourceNodeIP)
	write("")

	// Remove existing container
	runCmd(ctx, &logBuf, "docker", "rm", "-f", p.ContainerName)

	// Check local image timestamp
	imageReady := checkLocalImage(ctx, imageName, p.LastDeployed, &logBuf, write)

	if !imageReady {
		// Method 1: SSH transfer
		write("📥 Attempting SSH image transfer from %s...", p.SourceNodeIP)
		if transferred := sshTransfer(ctx, p.SourceNodeIP, imageName, &logBuf, write); transferred {
			imageReady = true
		} else {
			// Method 2: GitHub fallback
			write("⚠️  SSH transfer failed, building from GitHub...")
			if err := buildFromGitHub(ctx, imageName, p.Repository, &logBuf, write); err != nil {
				write("❌ GitHub build failed: %v", err)
				return exit(1)
			}
			imageReady = true
		}
	}

	if !imageReady {
		write("❌ No image source available")
		return exit(1)
	}

	// Build docker run args
	args := []string{"run", "-d", "--name", p.ContainerName, "--restart", "unless-stopped"}
	for _, port := range p.Ports {
		args = append(args, "-p", port)
	}
	for _, vol := range p.Volumes {
		args = append(args, "-v", vol)
	}
	for _, env := range p.EnvVars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", env.Key, env.Value))
	}
	args = append(args, imageName)

	write("🚀 Starting container %s...", p.ContainerName)
	if out, err := runCmdOutput(ctx, "docker", args...); err != nil {
		write("❌ Failed to start container: %v\n%s", err, out)
		return exit(1)
	}

	// Wait and verify
	time.Sleep(5 * time.Second)
	for i := 0; i < 6; i++ {
		out, _ := runCmdOutput(ctx, "docker", "ps", "--filter", "name="+p.ContainerName, "--format", "{{.Status}}")
		if strings.Contains(out, "Up") {
			write("✅ Container is running")
			logs, _ := runCmdOutput(ctx, "docker", "logs", p.ContainerName, "--tail", "20")
			write("\n📋 Container logs:\n%s", logs)
			return exit(0)
		}
		if i < 5 {
			write("⏳ Waiting for container... (%d/6)", i+1)
			time.Sleep(5 * time.Second)
		}
	}

	logs, _ := runCmdOutput(ctx, "docker", "logs", p.ContainerName, "--tail", "50")
	write("❌ Container failed to start\n%s", logs)
	return exit(1)
}

func checkLocalImage(ctx context.Context, imageName string, lastDeployed time.Time, buf *bytes.Buffer, write func(string, ...any)) bool {
	out, err := runCmdOutput(ctx, "docker", "image", "inspect", imageName, "--format", "{{.Created}}")
	if err != nil || strings.TrimSpace(out) == "" {
		write("📦 No local image found")
		return false
	}

	localTS, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(out))
	if err != nil {
		write("⚠️  Could not parse local image timestamp: %v", err)
		return false
	}

	if !localTS.Before(lastDeployed) {
		write("✅ Local image is up to date (built: %s)", localTS.Format(time.RFC3339))
		return true
	}

	write("⚠️  Local image is outdated (built: %s, expected >= %s)", localTS.Format(time.RFC3339), lastDeployed.Format(time.RFC3339))
	return false
}

func sshTransfer(ctx context.Context, sourceIP, imageName string, buf *bytes.Buffer, write func(string, ...any)) bool {
	sshArgs := []string{
		"-i", sshKey,
		"-o", "ConnectTimeout=" + sshTimeout,
		"-o", "StrictHostKeyChecking=no",
		"-o", "PasswordAuthentication=no",
		"-o", "BatchMode=yes",
		sourceIP,
		"docker save " + imageName,
	}

	sshCmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	sshOut, err := sshCmd.Output()
	if err != nil {
		write("⚠️  SSH transfer failed: %v", err)
		return false
	}

	loadCmd := exec.CommandContext(ctx, "docker", "load")
	loadCmd.Stdin = bytes.NewReader(sshOut)
	loadOut, err := loadCmd.CombinedOutput()
	if err != nil {
		write("⚠️  docker load failed: %v\n%s", err, string(loadOut))
		return false
	}

	write("✅ Image transferred via SSH")
	buf.Write(loadOut)
	return true
}

func buildFromGitHub(ctx context.Context, imageName, repoURL string, buf *bytes.Buffer, write func(string, ...any)) error {
	if err := os.MkdirAll(buildTmpDir, 0755); err != nil {
		return fmt.Errorf("failed to create build dir: %w", err)
	}

	// derive dir name from repo URL
	parts := strings.Split(strings.TrimSuffix(repoURL, ".git"), "/")
	projectDir := buildTmpDir + "/" + parts[len(parts)-1]

	os.RemoveAll(projectDir)

	write("📥 Cloning %s...", repoURL)
	if out, err := runCmdOutput(ctx, "git", "clone", repoURL, projectDir); err != nil {
		return fmt.Errorf("clone failed: %w\n%s", err, out)
	}

	write("🔨 Building Docker image...")
	if out, err := runCmdOutput(ctx, "docker", "build", "-t", imageName, projectDir); err != nil {
		os.RemoveAll(projectDir)
		return fmt.Errorf("build failed: %w\n%s", err, out)
	}

	os.RemoveAll(projectDir)
	write("✅ Image built from GitHub")
	return nil
}

func runCmd(ctx context.Context, buf *bytes.Buffer, name string, args ...string) {
	out, _ := runCmdOutput(ctx, name, args...)
	if out != "" {
		buf.WriteString(out + "\n")
	}
}

func runCmdOutput(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}