//go:build !windows

package main

import "syscall"

// sysProcDetach detaches the browser process from the console process group
// (posix: own session so closing the console does not kill the browser).
func sysProcDetach() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
