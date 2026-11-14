//go:build !windows

package server

import (
	"os/exec"
	"syscall"
)

// detachProcess puts the child in its own process group so it survives the
// console exiting (posix: setsid).
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
