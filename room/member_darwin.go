package room

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func bootID() (string, error) {
	if id, err := unix.Sysctl("kern.bootsessionuuid"); err == nil && id != "" {
		return id, nil
	}
	host, herr := os.Hostname()
	if herr != nil {
		return "", errors.New("room: neither a boot id nor a hostname: " + herr.Error())
	}
	return host, nil
}

func startTime() (int64, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getpid())
	if err != nil {
		return 0, fmt.Errorf("room: kern.proc.pid: %w", err)
	}
	t := kp.Proc.P_starttime
	return t.Sec*1_000_000 + int64(t.Usec), nil
}
