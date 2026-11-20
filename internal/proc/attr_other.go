//go:build !windows

package proc

import "syscall"

// backgroundAttr is nil because only Windows opens windows for child processes.
func backgroundAttr() *syscall.SysProcAttr {
	return nil
}
