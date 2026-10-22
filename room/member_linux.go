package room

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

var procStat = "/proc/self/stat"

func bootID() (string, error) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	host, herr := os.Hostname()
	if herr != nil {
		return "", errors.New("room: neither a boot id nor a hostname: " + herr.Error())
	}
	return host, nil
}

func startTime() (int64, error) {
	stat, err := os.ReadFile(procStat)
	if err != nil {
		return 0, fmt.Errorf("room: %s: %w", procStat, err)
	}
	end := strings.LastIndex(string(stat), ") ")
	if end < 0 {
		return 0, errors.New("room: /proc/self/stat has no exec name")
	}
	fields := strings.Split(string(stat[end+2:]), " ")
	if len(fields) < 20 {
		return 0, errors.New("room: /proc/self/stat is short")
	}
	start, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("room: /proc/self/stat starttime: %w", err)
	}
	return start, nil
}
