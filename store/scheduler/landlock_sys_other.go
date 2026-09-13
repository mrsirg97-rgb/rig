//go:build !linux || (!amd64 && !arm64)

package scheduler

import (
	"fmt"
	"runtime"
)

var landlockABIFn = func() (int, error) {
	return 0, fmt.Errorf("landlock: the profile is linux/amd64 or linux/arm64 (this build is %s/%s)", runtime.GOOS, runtime.GOARCH)
}

var landlockRestrictFn = func(spec LandlockSpec) error {
	return fmt.Errorf("landlock: the profile is linux/amd64 or linux/arm64 (this build is %s/%s)", runtime.GOOS, runtime.GOARCH)
}
