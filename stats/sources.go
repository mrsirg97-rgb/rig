package stats

import (
	"sort"
	"strconv"
	"strings"
)

func parseUptime(text string) (float64, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return 0, false
	}
	n, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func parseLoadavg(text string) ([3]float64, bool) {
	var load [3]float64
	fields := strings.Fields(text)
	if len(fields) < 3 {
		return load, false
	}
	for i := range load {
		n, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return load, false
		}
		load[i] = n
	}
	return load, true
}

func parseMeminfo(text string) *Memory {
	var total, avail uint64
	have := 0
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total, have = n, have|1
		case "MemAvailable":
			avail, have = n, have|2
		}
	}
	if have != 3 {
		return nil
	}
	return &Memory{TotalKiB: total, AvailableKiB: avail}
}

func parseCPU(text string) *counters {
	c := &counters{}
	found := false
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch {
		case fields[0] == "cpu":
			v, ok := jiffies(fields[1:])
			if !ok {
				return nil
			}
			var total, idle uint64
			for i, n := range v {
				total += n
				if i == 3 || i == 4 {
					idle += n
				}
			}
			c.total, c.idle, found = total, idle, true
		case strings.HasPrefix(fields[0], "cpu") && len(fields) > 1:
			c.cores++
		}
	}
	if !found {
		return nil
	}
	return c
}

func jiffies(fields []string) ([]uint64, bool) {
	v := make([]uint64, 0, 8)
	for _, f := range fields {
		if len(v) == 8 {
			break
		}
		n, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return nil, false
		}
		v = append(v, n)
	}
	if len(v) < 8 {
		return nil, false
	}
	return v, true
}

func cpuPercent(prev, cur counters) (float64, bool) {
	if cur.total <= prev.total || cur.idle < prev.idle {
		return 0, false
	}
	crossed := cur.total - prev.total
	idle := cur.idle - prev.idle
	if crossed == 0 || idle > crossed {
		return 0, false
	}
	return 100 * float64(crossed-idle) / float64(crossed), true
}

type tempInput struct {
	label string
	c     float64
}

type chipTemps struct {
	name   string
	device string
	inputs []tempInput
}

func disambiguate(chips []chipTemps) []chipTemps {
	count := make(map[string]int, len(chips))
	for _, c := range chips {
		count[c.name]++
	}
	for i := range chips {
		if count[chips[i].name] > 1 && chips[i].device != "" && chips[i].device != chips[i].name {
			chips[i].name = chips[i].device
		}
	}
	return chips
}

var dieChips = map[string]bool{"k10temp": true, "coretemp": true}

var packageChips = map[string]bool{"asusec": true, "asus": true}

func dropPackageChips(chips []chipTemps) []chipTemps {
	die := false
	for _, c := range chips {
		if dieChips[c.name] {
			die = true
			break
		}
	}
	if !die {
		return chips
	}
	kept := make([]chipTemps, 0, len(chips))
	for _, c := range chips {
		if packageChips[c.name] {
			continue
		}
		kept = append(kept, c)
	}
	return kept
}

func pickTemps(chips []chipTemps) []Temp {
	var rows []Temp
	chips = disambiguate(dropPackageChips(chips))
	for _, ch := range chips {
		var best *tempInput
		for i := range ch.inputs {
			if ch.inputs[i].label == "" {
				continue
			}
			if best == nil || ch.inputs[i].c > best.c {
				best = &ch.inputs[i]
			}
		}
		if best == nil {
			continue
		}
		rows = append(rows, Temp{Chip: ch.name, Label: best.label, C: best.c})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].C > rows[j].C })
	if len(rows) > bandRows {
		rows = rows[:bandRows]
	}
	return rows
}

const nvidiaFields = "index,name,utilization.gpu,temperature.gpu,power.draw,memory.used,memory.total"

func parseNvidiaCSV(text string) []GPU {
	var rows []GPU
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Split(line, ",")
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		if len(fields) != 7 {
			continue
		}
		index, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		row := GPU{Index: index, Name: fields[1]}
		if n, ok := number(fields[2]); ok {
			row.UtilPct = &n
		}
		if n, ok := number(fields[3]); ok {
			row.TempC = &n
		}
		if n, ok := number(fields[4]); ok {
			row.PowerW = &n
		}
		if n, ok := number(fields[5]); ok {
			used := uint64(n)
			row.MemUsedMiB = &used
		}
		if n, ok := number(fields[6]); ok {
			total := uint64(n)
			row.MemTotalMiB = &total
		}
		rows = append(rows, row)
		if len(rows) == gpuRows {
			break
		}
	}
	return rows
}

func number(field string) (float64, bool) {
	if field == "" || strings.HasPrefix(field, "[") {
		return 0, false
	}
	n, err := strconv.ParseFloat(field, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

type mount struct {
	device string
	path   string
	fstype string
}

var realFSTypes = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true,
	"xfs": true, "btrfs": true, "zfs": true, "f2fs": true, "jfs": true,
}

func parseMounts(text string) []mount {
	var ms []mount
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		ms = append(ms, mount{
			device: strings.ReplaceAll(fields[0], `\040`, " "),
			path:   strings.ReplaceAll(fields[1], `\040`, " "),
			fstype: fields[2],
		})
	}
	return ms
}

func diskRows(mounts []mount, size func(m mount) (used, total uint64, ok bool)) []Disk {
	seen := make(map[string]mount)
	for _, m := range mounts {
		if !realFSTypes[m.fstype] {
			continue
		}
		if held, ok := seen[m.device]; ok && len(held.path) <= len(m.path) {
			continue
		}
		seen[m.device] = m
	}
	rows := make([]Disk, 0, len(seen))
	for _, m := range seen {
		used, total, ok := size(m)
		if !ok || total == 0 {
			continue
		}
		rows = append(rows, Disk{Path: m.path, Device: m.device, UsedBytes: used, TotalBytes: total})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].TotalBytes != rows[j].TotalBytes {
			return rows[i].TotalBytes > rows[j].TotalBytes
		}
		return rows[i].Device < rows[j].Device
	})
	if len(rows) > bandRows {
		rows = rows[:bandRows]
	}
	return rows
}

var plumbingPrefixes = []string{"veth", "br-", "docker", "virbr", "tap"}

func pickLinks(cs []linkCounters) []linkCounters {
	kept := make([]linkCounters, 0, len(cs))
	for _, c := range cs {
		if c.iface == "lo" || hasPlumbingPrefix(c.iface) {
			continue
		}
		if c.rxBytes == 0 && c.txBytes == 0 {
			continue
		}
		kept = append(kept, c)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i].rxBytes+kept[i].txBytes, kept[j].rxBytes+kept[j].txBytes
		if a != b {
			return a > b
		}
		return kept[i].iface < kept[j].iface
	})
	if len(kept) > bandRows {
		kept = kept[:bandRows]
	}
	return kept
}

func hasPlumbingPrefix(iface string) bool {
	for _, p := range plumbingPrefixes {
		if strings.HasPrefix(iface, p) {
			return true
		}
	}
	return false
}
