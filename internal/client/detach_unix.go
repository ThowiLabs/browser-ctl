//go:build !windows

package client

import (
	"os/exec"
	"syscall"
)

// detachedAttr returns process attributes that start the daemon in its
// own session, detached from the parent.
func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

var _ = exec.Command
