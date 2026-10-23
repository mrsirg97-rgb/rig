//go:build linux

package execwrap

import "syscall"

func ParentDeathAttr(attr *syscall.SysProcAttr) {
	attr.Pdeathsig = syscall.SIGKILL
}
