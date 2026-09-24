//go:build !linux

package scheduler

import "syscall"

func spawnSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
