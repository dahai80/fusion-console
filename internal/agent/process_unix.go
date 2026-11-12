//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
)

// setProcessGroup gives the child its own process group so the whole tree
// can be killed on shutdown/timeout/budget breach.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree signals the child's process group (negative pid).
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}
