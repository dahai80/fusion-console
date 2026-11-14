//go:build windows

package server

import "os/exec"

// detachProcess is a no-op on windows: the detached web server is started
// via its own service path there.
func detachProcess(cmd *exec.Cmd) {}
