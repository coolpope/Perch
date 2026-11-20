package proc

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// backgroundAttr keeps Windows from giving each console program, such as
// git.exe or gopls.exe, a console window of its own: Perch is a GUI program,
// so it has no console for them to share.
func backgroundAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
