package deploy

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
)

type Deployment struct {
    Repository    string
    Branch        string
    ImageName     string
    ContainerName string
    Ports         []string
    EnvVars       map[string]string
}

func (d *Deployment) Execute(workDir string) error {
    // Create deployment directory
    deployDir := filepath.Join(workDir, d.ContainerName)
    if err := os.MkdirAll(deployDir, 0755); err != nil {
        return fmt.Errorf("failed to create deploy dir: %w", err)
    }

    // Clone repository
    if err := d.cloneRepo(deployDir); err != nil {
        return fmt.Errorf("failed to clone repo: %w", err)
    }

    // Build Docker image
    if err := d.buildImage(deployDir); err != nil {
        return fmt.Errorf("failed to build image: %w", err)
    }

    // Stop and remove old container
    if err := d.stopContainer(); err != nil {
        return fmt.Errorf("failed to stop container: %w", err)
    }

    // Run new container
    if err := d.runContainer(); err != nil {
        return fmt.Errorf("failed to run container: %w", err)
    }

    return nil
}

// cloneRepo clones the repository to the deployment directory
func (d *Deployment) cloneRepo(deployDir string) error {
    // Check if repo already exists
    gitDir := filepath.Join(deployDir, ".git")
    if _, err := os.Stat(gitDir); err == nil {
        // Repo exists, pull latest
        cmd := exec.Command("git", "-C", deployDir, "pull", "origin", d.Branch)
        if out, err := cmd.CombinedOutput(); err != nil {
            return fmt.Errorf("git pull failed: %w\n%s", err, out)
        }
        return nil
    }

    // Clone fresh
    cmd := exec.Command("git", "clone", "--branch", d.Branch, "--depth", "1", d.Repository, deployDir)
    if out, err := cmd.CombinedOutput(); err != nil {
        return fmt.Errorf("git clone failed: %w\n%s", err, out)
    }
    return nil
}

// buildImage builds the Docker image
func (d *Deployment) buildImage(deployDir string) error {
    cmd := exec.Command("docker", "build", "-t", d.ImageName, ".")
    cmd.Dir = deployDir
    
    if out, err := cmd.CombinedOutput(); err != nil {
        return fmt.Errorf("docker build failed: %w\n%s", err, out)
    }
    return nil
}

// stopContainer stops and removes the existing container if running
func (d *Deployment) stopContainer() error {
    // Check if container exists
    cmd := exec.Command("docker", "ps", "-a", "--filter", "name="+d.ContainerName, "--format", "{{.Names}}")
    out, err := cmd.Output()
    if err != nil {
        // Container doesn't exist or error, continue
        return nil
    }

    if strings.TrimSpace(string(out)) == "" {
        // Container doesn't exist
        return nil
    }

    // Stop container
    stopCmd := exec.Command("docker", "stop", d.ContainerName)
    if out, err := stopCmd.CombinedOutput(); err != nil {
        // Log but continue - maybe it's already stopped
        fmt.Printf("Warning: docker stop failed: %v\n%s\n", err, out)
    }

    // Remove container
    rmCmd := exec.Command("docker", "rm", d.ContainerName)
    if out, err := rmCmd.CombinedOutput(); err != nil {
        return fmt.Errorf("docker rm failed: %w\n%s", err, out)
    }

    return nil
}

// runContainer runs the new container
func (d *Deployment) runContainer() error {
    args := []string{
        "run",
        "-d",
        "--name", d.ContainerName,
        "--restart", "always",
    }

    // Add port mappings
    for _, port := range d.Ports {
        args = append(args, "-p", port)
    }

    // Add environment variables
    for key, value := range d.EnvVars {
        args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
    }

    // Add image name
    args = append(args, d.ImageName)

    cmd := exec.Command("docker", args...)
    if out, err := cmd.CombinedOutput(); err != nil {
        return fmt.Errorf("docker run failed: %w\n%s", err, out)
    }

    return nil
}

// Stop removes the container completely (for cleanup)
func (d *Deployment) Stop() error {
    return d.stopContainer()
}

// GetStatus checks if the container is running
func (d *Deployment) GetStatus() (string, error) {
    cmd := exec.Command("docker", "ps", "--filter", "name="+d.ContainerName, "--format", "{{.Status}}")
    out, err := cmd.Output()
    if err != nil {
        return "unknown", err
    }
    
    status := strings.TrimSpace(string(out))
    if status == "" {
        return "not running", nil
    }
    return status, nil
}

// GetLogs returns the last N lines of container logs
func (d *Deployment) GetLogs(lines int) (string, error) {
    cmd := exec.Command("docker", "logs", "--tail", fmt.Sprintf("%d", lines), d.ContainerName)
    out, err := cmd.CombinedOutput()
    if err != nil {
        return "", fmt.Errorf("failed to get logs: %w", err)
    }
    return string(out), nil
}

// Restart restarts the container
func (d *Deployment) Restart() error {
    cmd := exec.Command("docker", "restart", d.ContainerName)
    if out, err := cmd.CombinedOutput(); err != nil {
        return fmt.Errorf("docker restart failed: %w\n%s", err, out)
    }
    return nil
}

// GetContainerID returns the container ID
func (d *Deployment) GetContainerID() (string, error) {
    cmd := exec.Command("docker", "ps", "-a", "--filter", "name="+d.ContainerName, "--format", "{{.ID}}")
    out, err := cmd.Output()
    if err != nil {
        return "", err
    }
    return strings.TrimSpace(string(out)), nil
}