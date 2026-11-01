//go:build windows

package cli

import (
	"bufio"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/kjFriday/local-verse-proxy/internal/args"
)

var procList = windows.NewLazySystemDLL("kernel32.dll").
	NewProc("GetConsoleProcessList")

// clicked says whether lvp has its console window to itself, as when
// someone double-clicks lvp.exe: then the window closes the moment lvp
// ends, before anyone can read it.
func clicked() bool {
	if procList.Find() != nil {
		return false
	}
	var ids [2]uint32
	n, _, _ := procList.Call(uintptr(unsafe.Pointer(&ids[0])), 2)
	return n == 1
}

// pauseIfClicked waits for Enter when lvp was double-clicked, so the
// help stays on screen.
func pauseIfClicked(std args.Std) {
	if std.In != os.Stdin || !clicked() {
		return
	}
	fmt.Fprint(std.Err, "\nlvp runs in a terminal: open PowerShell in "+
		"this folder and type .\\lvp help\nPress Enter to close this "+
		"window.")
	bufio.NewReader(os.Stdin).ReadString('\n')
}
