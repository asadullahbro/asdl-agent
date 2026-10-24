//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
	"time"
)

// killWithChildren makes cancelling cmd's context kill its whole process group,
// and lets Wait return even if a stray child still holds the output pipes.
func killWithChildren(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 10 * time.Second
}
