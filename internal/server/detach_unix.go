//go:build !windows

package server

import "syscall"

// detachProcAttr detaches the spawned gateway process into its own process
// group so a console restart does not kill it (POSIX: setsid semantics via
// Setpgid; the console never signals that group afterwards).
func detachProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
