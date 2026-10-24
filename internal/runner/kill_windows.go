//go:build windows

package runner

import (
	"os/exec"
	"time"
)

func killWithChildren(cmd *exec.Cmd) { cmd.WaitDelay = 10 * time.Second }
