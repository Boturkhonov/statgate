//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func init() {
	// Enable ANSI virtual terminal processing so that escape sequences for
	// cursor movement and screen clearing work in cmd.exe and PowerShell.
	// Windows Terminal and VS Code terminals already support ANSI natively,
	// but the legacy console host requires explicit opt-in via SetConsoleMode.
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err == nil {
		_ = windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}
