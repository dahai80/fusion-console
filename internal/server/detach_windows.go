//go:build windows

package server

import "syscall"

// detachProcAttr: on Windows a child already outlives the parent console by
// default, so no CREATE_* flags are needed for the detach contract.
func detachProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{} }
