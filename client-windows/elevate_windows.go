//go:build windows

package main

import (
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// isAdmin reports whether the process holds the local Administrators group,
// which is required to create the TUN adapter and modify the routing table.
func isAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY, 2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0, &sid)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)
	token := windows.Token(0) // current process token
	member, err := token.IsMember(sid)
	return err == nil && member
}

// relaunchElevated restarts this executable with a UAC elevation prompt.
func relaunchElevated() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cwd, _ := os.Getwd()
	args := strings.Join(os.Args[1:], " ")

	verbPtr, _ := syscall.UTF16PtrFromString("runas")
	exePtr, _ := syscall.UTF16PtrFromString(exe)
	cwdPtr, _ := syscall.UTF16PtrFromString(cwd)
	var argPtr *uint16
	if args != "" {
		argPtr, _ = syscall.UTF16PtrFromString(args)
	}
	windows.ShellExecute(0, verbPtr, exePtr, argPtr, cwdPtr, windows.SW_SHOWNORMAL)
}
