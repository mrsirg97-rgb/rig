//go:build !linux && !darwin

package room

import (
	"errors"
	"runtime"
)

func bootID() (string, error) {
	return "", errors.New("room: no process identity on " + runtime.GOOS)
}

func startTime() (int64, error) {
	return 0, errors.New("room: no process identity on " + runtime.GOOS)
}
