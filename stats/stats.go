// Package stats reads the machine's vitals from the world: the load, the
// memory, the temperatures, the GPUs, the filesystems and the links. The
// dashboard's room page asks what the box is doing (SPEC_ROOM's system
// stats); nothing here is replayed from a store, and a source the box does
// not have is absent from the report rather than a zero that lies.
package stats

import (
	"context"
	"time"
)

const (
	// bandRows caps each sensor section at the four hottest, largest, or
	// busiest rows: the band shares the phone's screen with one model card,
	// whose served table shows twelve rows (frontend/web's servedShown).
	bandRows = 4
	// gpuRows bounds the GPU rows at a dense PCIe box's width. A box with
	// more cards still holds them; they just do not make the band.
	gpuRows = 8
	// gpuTimeout is half the room page's four-second poll (app.js's
	// ROOM_REFRESH_MS), the only sampler: a source slower than that is late
	// for the read that asked for it.
	gpuTimeout = 2 * time.Second
)

// Sample is one walk of the world. Its counters are absolute; every rate a
// Report answers is a difference between two samples.
type Sample struct {
	at     time.Time
	uptime float64
	load   *[3]float64
	mem    *Memory
	cpu    *counters
	temps  []Temp
	gpus   []GPU
	disks  []Disk
	links  []linkCounters
}

// Read walks every source once. It never fails as a whole: a source that is
// missing, unreadable, or slow past gpuTimeout is simply absent from the
// sample. The context bounds the one source that runs a program.
func Read(ctx context.Context) *Sample {
	s := &Sample{at: time.Now()}
	walk(ctx, s)
	return s
}

// Report is what the dashboard renders. prev is the last sample: the CPU's
// percent and a link's bytes per second are answered against it, and a first
// read after a page opens carries none of them rather than a number invented
// from one point. A box with no source renders nothing: the zero Report
// marshals to an empty object.
type Report struct {
	UptimeS float64     `json:"uptime_s,omitempty"`
	Load    *[3]float64 `json:"load,omitempty"`
	Memory  *Memory     `json:"memory,omitempty"`
	CPU     *CPU        `json:"cpu,omitempty"`
	Temps   []Temp      `json:"temps,omitempty"`
	Gpus    []GPU       `json:"gpus,omitempty"`
	Disks   []Disk      `json:"disks,omitempty"`
	Links   []Link      `json:"links,omitempty"`
}

// CPU is the processor: its core count, and the busy share since the last
// sample.
type CPU struct {
	Cores   int      `json:"cores,omitempty"`
	Percent *float64 `json:"percent,omitempty"`
}

// Memory is the machine's RAM, in kilobytes, as /proc/meminfo counts it.
type Memory struct {
	TotalKiB     uint64 `json:"total_kib"`
	AvailableKiB uint64 `json:"available_kib"`
}

// Temp is one chip's hottest labelled input: a chip's several inputs are one
// die's several zones, and the hottest is the news.
type Temp struct {
	Chip  string  `json:"chip"`
	Label string  `json:"label"`
	C     float64 `json:"c"`
}

// GPU is one card, as nvidia-smi reports it. A field the card does not answer
// is absent, not zero.
type GPU struct {
	Index       int      `json:"index"`
	Name        string   `json:"name,omitempty"`
	UtilPct     *float64 `json:"util,omitempty"`
	TempC       *float64 `json:"temp,omitempty"`
	PowerW      *float64 `json:"power_w,omitempty"`
	MemUsedMiB  *uint64  `json:"mem_used_mib,omitempty"`
	MemTotalMiB *uint64  `json:"mem_total_mib,omitempty"`
}

// Disk is one real filesystem, deduplicated by device: a bind mount and the
// mount it binds repeat the same device, and one row says it once.
type Disk struct {
	Path       string `json:"path"`
	Device     string `json:"device"`
	UsedBytes  uint64 `json:"used_bytes"`
	TotalBytes uint64 `json:"total_bytes"`
}

// Link is one network interface that has moved a byte.
type Link struct {
	Iface   string   `json:"iface"`
	RxBytes uint64   `json:"rx_bytes"`
	TxBytes uint64   `json:"tx_bytes"`
	RxRate  *float64 `json:"rx_rate,omitempty"`
	TxRate  *float64 `json:"tx_rate,omitempty"`
}

// counters is the CPU's jiffies, as /proc/stat's aggregate line counts them:
// the total, the idle share of it, and the core count beside it.
type counters struct {
	total uint64
	idle  uint64
	cores int
}

// linkCounters is one interface's byte counters, as its sysfs statistics
// directory holds them.
type linkCounters struct {
	iface   string
	rxBytes uint64
	txBytes uint64
}

// Report folds the sample into its shapes, answering the rates against prev.
func (s *Sample) Report(prev *Sample) Report {
	r := Report{UptimeS: s.uptime, Load: s.load, Memory: s.mem,
		Temps: s.temps, Gpus: s.gpus, Disks: s.disks}
	if s.cpu != nil {
		r.CPU = &CPU{Cores: s.cpu.cores}
		if prev != nil && prev.cpu != nil {
			if p, ok := cpuPercent(*prev.cpu, *s.cpu); ok {
				r.CPU.Percent = &p
			}
		}
	}
	secs := 0.0
	if prev != nil {
		secs = s.at.Sub(prev.at).Seconds()
	}
	for _, c := range s.links {
		l := Link{Iface: c.iface, RxBytes: c.rxBytes, TxBytes: c.txBytes}
		if prev != nil && secs > 0 {
			if old, ok := prev.link(c.iface); ok {
				l.RxRate = byteRate(old.rxBytes, c.rxBytes, secs)
				l.TxRate = byteRate(old.txBytes, c.txBytes, secs)
			}
		}
		r.Links = append(r.Links, l)
	}
	return r
}

func (s *Sample) link(iface string) (linkCounters, bool) {
	for _, c := range s.links {
		if c.iface == iface {
			return c, true
		}
	}
	return linkCounters{}, false
}

// byteRate answers bytes per second across an elapsed span. A counter that
// fell — the interface was reset, the driver reloaded — answers no rate: the
// window is the machine's, not this program's.
func byteRate(prev, cur uint64, secs float64) *float64 {
	if cur < prev || secs <= 0 {
		return nil
	}
	rate := float64(cur-prev) / secs
	return &rate
}
