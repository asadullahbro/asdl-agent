package deploy

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
)

type Deployment struct {
    Repository string
    Branch     string
    ImageName  string
    ContainerName string
    Ports      []string
    EnvVars    map[string]string
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