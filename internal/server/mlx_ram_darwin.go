//go:build darwin

package server

import "golang.org/x/sys/unix"

// ramGB reports unified memory in GB (0 when undetectable). darwin-only:
// hw.memsize is the Apple Silicon unified memory size in bytes.
func ramGB() int {
	v, err := unix.SysctlUint64("hw.memsize")
	if err != nil || v == 0 {
		return 0
	}
	return int(v / 1_000_000_000)
}
