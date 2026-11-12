//go:build windows

package main

import "syscall"

// sysProcDetach hides the browser-launcher console window on Windows.
func sysProcDetach() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
