//go:build linux

package stats

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	hwmonRoot = "/sys/class/hwmon"
	linksRoot = "/sys/class/net"
)

// walk reads the box's sources once. Every read is best effort by rule: the
// source that is not there renders no row, so a box missing half of them
// still answers with the half it has.
func walk(ctx context.Context, s *Sample) {
	if text, err := os.ReadFile("/proc/uptime"); err == nil {
		s.uptime, _ = parseUptime(string(text))
	}
	if text, err := os.ReadFile("/proc/loadavg"); err == nil {
		if load, ok := parseLoadavg(string(text)); ok {
			s.load = &load
		}
	}
	if text, err := os.ReadFile("/proc/meminfo"); err == nil {
		s.mem = parseMeminfo(string(text))
	}
	if text, err := os.ReadFile("/proc/stat"); err == nil {
		s.cpu = parseCPU(string(text))
	}
	s.temps = readTemps(hwmonRoot)
	s.disks = readDisks()
	s.links = readLinks(linksRoot)
	s.gpus = readGPUs(ctx)
}

// readTemps walks the hwmon tree: one chip per directory, named by its own
// name file.
func readTemps(root string) []Temp {
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	chips := make([]chipTemps, 0, len(dirs))
	for _, d := range dirs {
		dir := filepath.Join(root, d.Name())
		name := strings.TrimSpace(firstLine(filepath.Join(dir, "name")))
		if name == "" {
			name = d.Name()
		}
		inputs := hwmonSensors(dir)
		if len(inputs) == 0 {
			continue
		}
		chips = append(chips, chipTemps{name: name, device: deviceBase(dir), inputs: inputs})
	}
	return pickTemps(chips)
}

// deviceBase names the sysfs device a chip sits under — "nvme0" for a drive's
// hwmon — or nothing when the chip states no device link.
func deviceBase(dir string) string {
	target, err := filepath.EvalSymlinks(filepath.Join(dir, "device"))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

// hwmonSensors reads one chip's labelled sensors: a tempN_label and its
// tempN_input, the latter in millidegrees Celsius. A sensor the chip does not
// label says nothing an operator would read.
func hwmonSensors(dir string) []tempInput {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var inputs []tempInput
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), "_label") {
			continue
		}
		sensor := strings.TrimSuffix(e.Name(), "_label")
		if !strings.HasPrefix(sensor, "temp") {
			continue
		}
		label := strings.TrimSpace(firstLine(filepath.Join(dir, e.Name())))
		text, err := os.ReadFile(filepath.Join(dir, sensor+"_input"))
		if err != nil {
			continue
		}
		milli, err := strconv.ParseInt(strings.TrimSpace(string(text)), 10, 64)
		if err != nil {
			continue
		}
		inputs = append(inputs, tempInput{label: label, c: float64(milli) / 1000})
	}
	return inputs
}

// readLinks walks /sys/class/net, reading each interface's byte counters. An
// interface with no statistics directory is not a link the kernel counts.
func readLinks(root string) []linkCounters {
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	cs := make([]linkCounters, 0, len(dirs))
	for _, d := range dirs {
		rx, ok := readCounter(filepath.Join(root, d.Name(), "statistics", "rx_bytes"))
		if !ok {
			continue
		}
		tx, _ := readCounter(filepath.Join(root, d.Name(), "statistics", "tx_bytes"))
		cs = append(cs, linkCounters{iface: d.Name(), rxBytes: rx, txBytes: tx})
	}
	return pickLinks(cs)
}

func readCounter(path string) (uint64, bool) {
	text, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(text)), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// readDisks asks the kernel for the real filesystems' blocks.
func readDisks() []Disk {
	text, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil
	}
	return diskRows(parseMounts(string(text)), statfs)
}

func statfs(m mount) (used, total uint64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(m.path, &st); err != nil || st.Bsize <= 0 || st.Blocks == 0 {
		return 0, 0, false
	}
	bsize := uint64(st.Bsize)
	return uint64(st.Blocks-st.Bfree) * bsize, uint64(st.Blocks) * bsize, true
}

func firstLine(path string) string {
	text, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.SplitN(string(text), "\n", 2)[0]
}
