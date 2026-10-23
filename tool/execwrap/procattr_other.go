//go:build !linux

package execwrap

import "syscall"

func ParentDeathAttr(*syscall.SysProcAttr) {}
