package stats

import (
	"context"
	"time"
)

const (
	bandRows   = 4
	gpuRows    = 8
	gpuTimeout = 2 * time.Second
)

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

func Read(ctx context.Context) *Sample {
	s := &Sample{at: time.Now()}
	walk(ctx, s)
	return s
}

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

type CPU struct {
	Cores   int      `json:"cores,omitempty"`
	Percent *float64 `json:"percent,omitempty"`
}

type Memory struct {
	TotalKiB     uint64 `json:"total_kib"`
	AvailableKiB uint64 `json:"available_kib"`
}

type Temp struct {
	Chip  string  `json:"chip"`
	Label string  `json:"label"`
	C     float64 `json:"c"`
}

type GPU struct {
	Index       int      `json:"pci_index"`
	Name        string   `json:"name,omitempty"`
	UtilPct     *float64 `json:"util,omitempty"`
	TempC       *float64 `json:"temp,omitempty"`
	PowerW      *float64 `json:"power_w,omitempty"`
	MemUsedMiB  *uint64  `json:"mem_used_mib,omitempty"`
	MemTotalMiB *uint64  `json:"mem_total_mib,omitempty"`
}

type Disk struct {
	Path       string `json:"path"`
	Device     string `json:"device"`
	UsedBytes  uint64 `json:"used_bytes"`
	TotalBytes uint64 `json:"total_bytes"`
}

type Link struct {
	Iface   string   `json:"iface"`
	RxBytes uint64   `json:"rx_bytes"`
	TxBytes uint64   `json:"tx_bytes"`
	RxRate  *float64 `json:"rx_rate,omitempty"`
	TxRate  *float64 `json:"tx_rate,omitempty"`
}

type counters struct {
	total uint64
	idle  uint64
	cores int
}

type linkCounters struct {
	iface   string
	rxBytes uint64
	txBytes uint64
}

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

func byteRate(prev, cur uint64, secs float64) *float64 {
	if cur < prev || secs <= 0 {
		return nil
	}
	rate := float64(cur-prev) / secs
	return &rate
}
