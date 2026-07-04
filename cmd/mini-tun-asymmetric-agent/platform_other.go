//go:build !windows

package main

// On non-Windows the agent runs without UAC self-elevation (Linux uses the CLI
// or runs as root directly). These stubs keep the cross-platform build green.

func isAdmin() bool        { return true }
func relaunchElevated()    {}
func processAlive(int) bool { return true }
