//go:build windows

package agent

import (
	"os/exec"
	"strconv"
)

// setProcessGroup is a no-op on Windows; killTree uses taskkill /T instead.
func setProcessGroup(cmd *exec.Cmd) {}

// killTree terminates the child and its tree via taskkill.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", itoa(cmd.Process.Pid))
	_ = kill.Run()
	_ = cmd.Process.Kill()
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
