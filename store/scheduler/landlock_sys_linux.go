//go:build linux && (amd64 || arm64)

package scheduler

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	sysLandlockCreateRuleset = 444
	sysLandlockAddRule       = 445
	sysLandlockRestrictSelf  = 446
	landlockRulePathBeneath  = 1
	landlockCreateVersion    = 1
	landlockOPath            = 0x200000
	landlockPrSetNoNewPrivs  = 38
)

const (
	landlockExecute    uint64 = 1 << 0
	landlockWriteFile  uint64 = 1 << 1
	landlockReadFile   uint64 = 1 << 2
	landlockReadDir    uint64 = 1 << 3
	landlockRemoveDir  uint64 = 1 << 4
	landlockRemoveFile uint64 = 1 << 5
	landlockMakeChar   uint64 = 1 << 6
	landlockMakeDir    uint64 = 1 << 7
	landlockMakeReg    uint64 = 1 << 8
	landlockMakeSock   uint64 = 1 << 9
	landlockMakeFifo   uint64 = 1 << 10
	landlockMakeBlock  uint64 = 1 << 11
	landlockMakeSym    uint64 = 1 << 12
	landlockRefer      uint64 = 1 << 13
	landlockTruncate   uint64 = 1 << 14
)

const (
	landlockBindTCP    uint64 = 1 << 0
	landlockConnectTCP uint64 = 1 << 1
)

const (
	landlockScopeAbstractUnix uint64 = 1 << 0
	landlockScopeSignal       uint64 = 1 << 1
)

var landlockABIFn = landlockProbe
var landlockRestrictFn = landlockRestrict

type landlockRulesetAttr struct {
	handledAccessFS  uint64
	handledAccessNet uint64
	scoped           uint64
}

type landlockPathBeneathAttr struct {
	allowedAccess uint64
	parentFD      int32
}

type landlockGrant struct {
	path   string
	rights uint64
}

func landlockProbe() (int, error) {
	r, _, errno := syscall.Syscall(sysLandlockCreateRuleset, 0, 0, landlockCreateVersion)
	if errno != 0 {
		return 0, errno
	}
	return int(r), nil
}

func landlockFSAll(abi int) uint64 {
	m := landlockExecute | landlockWriteFile | landlockReadFile | landlockReadDir |
		landlockRemoveDir | landlockRemoveFile | landlockMakeChar | landlockMakeDir |
		landlockMakeReg | landlockMakeSock | landlockMakeFifo | landlockMakeBlock |
		landlockMakeSym
	if abi >= 2 {
		m |= landlockRefer
	}
	if abi >= 3 {
		m |= landlockTruncate
	}
	return m
}

func landlockGrants(spec LandlockSpec, fsAll uint64) ([]landlockGrant, error) {
	roDir := landlockReadFile | landlockReadDir | landlockExecute
	roExec := landlockReadFile | landlockExecute
	roRead := landlockReadFile | landlockReadDir
	devNode := landlockReadFile | landlockWriteFile
	shm := landlockReadFile | landlockWriteFile | landlockMakeReg | landlockMakeDir
	paths := []struct {
		name string
		path string
	}{
		{"cwd", spec.Cwd},
		{"scratch", spec.Scratch},
		{"binary", spec.Binary},
	}
	if spec.Kernel != "" {
		paths = append(paths, struct {
			name string
			path string
		}{"kernel", spec.Kernel})
	}
	for _, p := range paths {
		if err := landlockGrantExists(p.path); err != nil {
			return nil, err
		}
	}
	for i, b := range spec.Binds {
		src, _, err := parseBind(b, i)
		if err != nil {
			return nil, err
		}
		if err := landlockGrantExists(src); err != nil {
			return nil, fmt.Errorf("sandbox: sandboxBinds[%d]: %w", i, err)
		}
	}
	grants := []landlockGrant{
		{spec.Cwd, fsAll},
		{spec.Scratch, fsAll},
		{"/usr", roDir},
		{"/lib", roDir},
		{"/lib64", roDir},
		{"/bin", roDir},
		{"/sbin", roDir},
		{"/etc", roDir},
		{"/proc", roRead},
		{"/dev/null", devNode},
		{"/dev/zero", devNode},
		{"/dev/random", devNode},
		{"/dev/urandom", devNode},
		{"/dev/shm", shm},
	}
	if spec.Kernel != "" {
		grants = append(grants, landlockGrant{spec.Kernel, roDir})
	}
	grants = append(grants, landlockGrant{spec.Binary, roExec})
	for i, b := range spec.Binds {
		src, rw, _ := parseBind(b, i)
		if rw {
			grants = append(grants, landlockGrant{src, fsAll})
		} else {
			grants = append(grants, landlockGrant{src, roDir})
		}
	}
	return grants, nil
}

func landlockGrantExists(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("landlock: grant: %s: %v (a grant must exist before the restrict)", path, err)
	}
	return nil
}

func landlockRestrict(spec LandlockSpec) error {
	abi, err := landlockProbe()
	if err != nil {
		return fmt.Errorf("%s", LandlockProbeRefusal(err))
	}
	if abi < 4 {
		return fmt.Errorf("%s", LandlockABIRefusal(abi))
	}
	fsAll := landlockFSAll(abi)
	grants, err := landlockGrants(spec, fsAll)
	if err != nil {
		return err
	}
	netHandled := uint64(0)
	if abi >= 4 {
		netHandled = landlockBindTCP | landlockConnectTCP
	}
	scoped := uint64(0)
	attrSize := 16
	if abi >= 6 {
		scoped = landlockScopeSignal | landlockScopeAbstractUnix
		attrSize = 24
	}
	attr := landlockRulesetAttr{
		handledAccessFS: fsAll, handledAccessNet: netHandled, scoped: scoped,
	}
	fd, _, errno := syscall.Syscall(sysLandlockCreateRuleset, uintptr(unsafe.Pointer(&attr)), uintptr(attrSize), 0)
	if errno != 0 {
		return fmt.Errorf("landlock: create ruleset: %w", errno)
	}
	defer syscall.Close(int(fd))
	for _, g := range grants {
		if err := landlockAddRule(int(fd), g); err != nil {
			return err
		}
	}
	_, _, errno = syscall.Syscall6(syscall.SYS_PRCTL, landlockPrSetNoNewPrivs, 1, 0, 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("landlock: no_new_privs: %w", errno)
	}
	_, _, errno = syscall.Syscall(sysLandlockRestrictSelf, fd, 0, 0)
	if errno != 0 {
		return fmt.Errorf("landlock: restrict self: %w", errno)
	}
	return nil
}

func landlockAddRule(fd int, g landlockGrant) error {
	f, err := syscall.Open(g.path, landlockOPath|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("landlock: open grant %s: %w", g.path, err)
	}
	defer syscall.Close(f)
	rule := landlockPathBeneathAttr{allowedAccess: g.rights, parentFD: int32(f)}
	_, _, errno := syscall.Syscall(sysLandlockAddRule, uintptr(fd), landlockRulePathBeneath, uintptr(unsafe.Pointer(&rule)))
	if errno != 0 {
		return fmt.Errorf("landlock: add rule %s: %w", g.path, errno)
	}
	return nil
}
