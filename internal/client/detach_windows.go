//go:build windows

package client

import "syscall"

// Flags from the Win32 API (not in the stdlib syscall package):
// CREATE_NEW_PROCESS_GROUP 0x200, DETACHED_PROCESS 0x8.
const (
	createNewProcessGroup = 0x200
	detachedProcess       = 0x8
)

// detachedAttr returns process attributes that start the daemon detached
// from the parent console, in its own process group.
func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup | detachedProcess,
	}
}
