package stats

import (
	"context"
	"encoding/json"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"
)

func f64(t *testing.T, p *float64, want float64) {
	t.Helper()
	if p == nil {
		t.Fatalf("want %v, got no value", want)
	}
	if *p != want {
		t.Fatalf("want %v, got %v", want, *p)
	}
}

func TestTheLoadAndUptimeLinesParse(t *testing.T) {
	load, ok := parseLoadavg("1.00 0.59 0.93 25/1906 1768639\n")
	if !ok || load != [3]float64{1.00, 0.59, 0.93} {
		t.Fatalf("want 1.00 0.59 0.93, got %v (ok %v)", load, ok)
	}
	if _, ok := parseLoadavg(""); ok {
		t.Fatal("an empty load line answers no load")
	}
	if up, ok := parseUptime("257966.84 11430553.81\n"); !ok || up != 257966.84 {
		t.Fatalf("want the uptime's 257966.84, got %v (ok %v)", up, ok)
	}
	if _, ok := parseUptime("idle 0.0"); ok {
		t.Fatal("a line that does not count seconds answers no uptime")
	}
}

func TestTheMemoryPair(t *testing.T) {
	m := parseMeminfo("MemTotal:       98340812 kB\nMemFree:   100 kB\nMemAvailable:   67492620 kB\n")
	if m == nil || m.TotalKiB != 98340812 || m.AvailableKiB != 67492620 {
		t.Fatalf("want the pair the kernel states, got %+v", m)
	}
	if parseMeminfo("MemTotal:\t99 kB\nBuffers: 1 kB\n") != nil {
		t.Fatal("no MemAvailable line is no memory row (fail closed, not a zero)")
	}
}

func TestTheCPUJiffiesAndTheirDelta(t *testing.T) {
	c := parseCPU("cpu  1 1 1 10 1 1 1 1 0 0\ncpu0 5 0 0 9 1 0 0 0 0 0\ncpu1 4 0 0 8 1 0 0 0 0 0\nintr 1\n")
	if c == nil || c.cores != 2 || c.total != 17 || c.idle != 11 {
		t.Fatalf("want 2 cores over 17 jiffies with 11 idle, got %+v", c)
	}
	p, ok := cpuPercent(counters{total: 100, idle: 50}, counters{total: 200, idle: 100})
	if !ok || p != 50 {
		t.Fatalf("want a 50%% busy share, got %v (ok %v)", p, ok)
	}
	if _, ok := cpuPercent(counters{total: 200, idle: 100}, counters{total: 100, idle: 50}); ok {
		t.Fatal("a counter that fell answers no percent")
	}
	if _, ok := cpuPercent(counters{total: 100, idle: 50}, counters{total: 100, idle: 50}); ok {
		t.Fatal("no jiffies crossed answers no percent")
	}
	if _, ok := cpuPercent(counters{total: 100, idle: 50}, counters{total: 150, idle: 120}); ok {
		t.Fatal("an idle share larger than the window is a lie, not a percent")
	}
}

func TestOneRowPerChipAndTheHottestInputWins(t *testing.T) {
	chips := []chipTemps{
		{name: "k10temp", inputs: []tempInput{{"Tctl", 72.6}, {"Tccd1", 45.5}, {"Tccd2", 44.1}}},
		{name: "nvme", inputs: []tempInput{{"Composite", 43.8}, {"Sensor 1", 51.8}, {"Sensor 2", 46.9}}},
		{name: "eno2", inputs: []tempInput{{"PHY Temperature", 40.0}, {"MAC Temperature", 41.0}}},
	}
	got := pickTemps(chips)
	if len(got) != 3 {
		t.Fatalf("want one row per chip, got %+v", got)
	}
	if got[0].Chip != "k10temp" || got[0].Label != "Tctl" || got[0].C != 72.6 {
		t.Fatalf("the hottest chip leads, and its hottest labelled input speaks: %+v", got[0])
	}
	if got[1].Chip != "nvme" || got[1].Label != "Sensor 1" {
		t.Fatalf("want the nvme's hottest sensor, got %+v", got[1])
	}
	if len(pickTemps([]chipTemps{{name: "cold"}})) != 0 {
		t.Fatal("a chip with no inputs renders no row")
	}
	for i := 0; i < bandRows+3; i++ {
		chips = append(chips, chipTemps{name: "extra" + string(rune('a'+i)), inputs: []tempInput{{"T", 10 + float64(i)}}})
	}
	if n := len(pickTemps(chips)); n != bandRows {
		t.Fatalf("the band caps the chips at %d, got %d", bandRows, n)
	}
}

func TestTwoChipsThatShareANameSayWhich(t *testing.T) {
	got := pickTemps([]chipTemps{
		{name: "nvme", device: "nvme1", inputs: []tempInput{{"Sensor 1", 62.8}}},
		{name: "nvme", device: "nvme0", inputs: []tempInput{{"Sensor 2", 53.8}}},
		{name: "nvme", inputs: []tempInput{{"Composite", 40.0}}},
		{name: "k10temp", device: "0000:00:18.3", inputs: []tempInput{{"Tctl", 71.7}}},
	})
	if got[0].Chip != "k10temp" {
		t.Fatalf("a chip with a name to itself keeps it: %+v", got[0])
	}
	if got[1].Chip != "nvme1" || got[2].Chip != "nvme0" {
		t.Fatalf("two chips named nvme say which drive: %+v", got)
	}
	if got[3].Chip != "nvme" {
		t.Fatalf("a chip with no device to name itself by keeps the shared name: %+v", got[3])
	}
}

func TestTheBoardsPackageReadingYieldsToTheDiesOwnReport(t *testing.T) {
	both := pickTemps([]chipTemps{
		{name: "asusec", inputs: []tempInput{{"CPU Package", 73.0}}},
		{name: "k10temp", inputs: []tempInput{{"Tctl", 70.6}}},
		{name: "nvme0", inputs: []tempInput{{"Sensor 1", 59.9}}},
	})
	if len(both) != 2 {
		t.Fatalf("the board reads the same die the CPU reports: %+v", both)
	}
	if both[0].Chip != "k10temp" || both[1].Chip != "nvme0" {
		t.Fatalf("the die's own report survives: %+v", both)
	}
	alone := pickTemps([]chipTemps{{name: "asusec", inputs: []tempInput{{"CPU Package", 73.0}}}})
	if len(alone) != 1 || alone[0].Chip != "asusec" {
		t.Fatalf("a box with no die report still shows the board's: %+v", alone)
	}
	intel := pickTemps([]chipTemps{
		{name: "asus", inputs: []tempInput{{"CPU Package", 71.0}}},
		{name: "coretemp", inputs: []tempInput{{"Package id 0", 69.0}}},
	})
	if len(intel) != 1 || intel[0].Chip != "coretemp" {
		t.Fatalf("the rule is the chip, not the board: %+v", intel)
	}
}

func TestTheNvidiaCSV(t *testing.T) {
	gpus := parseNvidiaCSV(`0, NVIDIA GeForce RTX 5090, 36, 38, 221.98, 29121, 32607
1, NVIDIA GeForce RTX 3090, 9, 60, 149.19, 10997, 24576
2, NVIDIA GeForce RTX 5090, 53, 36, [N/A], 31945, 32607
`)
	if len(gpus) != 3 {
		t.Fatalf("want three cards, got %d", len(gpus))
	}
	if gpus[0].Index != 0 || gpus[0].Name != "NVIDIA GeForce RTX 5090" {
		t.Fatalf("want the first card named, got %+v", gpus[0])
	}
	f64(t, gpus[0].UtilPct, 36)
	f64(t, gpus[0].TempC, 38)
	f64(t, gpus[0].PowerW, 221.98)
	if gpus[0].MemUsedMiB == nil || *gpus[0].MemUsedMiB != 29121 {
		t.Fatalf("want 29121 MiB used, got %v", gpus[0].MemUsedMiB)
	}
	if gpus[2].PowerW != nil {
		t.Fatalf("an [N/A] power draw is absent, not zero: %v", *gpus[2].PowerW)
	}
	if n := len(parseNvidiaCSV("ERROR: Notification -- No devices were found.\n")); n != 0 {
		t.Fatalf("a driver that answers an error renders no cards, got %d", n)
	}
	body, err := json.Marshal(gpus)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"pci_index":0`) {
		t.Fatalf("the card's number is named for the enumeration it came from: %s", body)
	}
	var many strings.Builder
	for i := 0; i < gpuRows+5; i++ {
		many.WriteString("0, card, 1, 2, 3, 4, 5\n")
	}
	if n := len(parseNvidiaCSV(many.String())); n != gpuRows {
		t.Fatalf("the band caps the cards at %d, got %d", gpuRows, n)
	}
}

func TestTheDisksAreTheRealFilesystemsOnceEach(t *testing.T) {
	mounts := parseMounts(`/dev/nvme0n1p2 / ext4 rw,relatime 0 0
/dev/nvme0n1p2 /home/operator ext4 rw,relatime 0 0
/dev/nvme1n1p1 /mnt/data ext4 rw,relatime 0 0
/dev/nvme1n1p1 /var/lib/docker/overlay2/x/merged overlay rw 0 0
tmpfs /run tmpfs rw 0 0
proc /proc proc rw 0 0
udev /dev devtmpfs rw 0 0
/dev/sr0 /snap/x squashfs ro 0 0
a garbage line
`)
	sizes := func(m mount) (used, total uint64, ok bool) {
		switch m.path {
		case "/":
			return 473, 915, true
		case "/mnt/data":
			return 1900, 3600, true
		}
		return 0, 0, false
	}
	rows := diskRows(mounts, sizes)
	if len(rows) != 2 {
		t.Fatalf("one row per real device, no overlay/tmpfs/squashfs churn: %+v", rows)
	}
	if rows[0].Device != "/dev/nvme1n1p1" || rows[0].Path != "/mnt/data" {
		t.Fatalf("the biggest filesystem leads: %+v", rows[0])
	}
	if rows[1].Device != "/dev/nvme0n1p2" || rows[1].Path != "/" {
		t.Fatalf("the device appears once, at its shortest mount: %+v", rows[1])
	}
	if rows[0].UsedBytes != 1900 || rows[0].TotalBytes != 3600 {
		t.Fatalf("want the sizes the seam answered, got %+v", rows[0])
	}
}

func TestTheLinksThatMoveBytes(t *testing.T) {
	counters := []linkCounters{
		{iface: "wlp69s0", rxBytes: 292310, txBytes: 7253},
		{iface: "lo", rxBytes: 3883, txBytes: 3883},
		{iface: "tailscale0", rxBytes: 0, txBytes: 9},
		{iface: "eno1", rxBytes: 0, txBytes: 0},
		{iface: "veth21c9101", rxBytes: 500, txBytes: 500},
		{iface: "br-1af0c3fc9fc6", rxBytes: 400, txBytes: 400},
		{iface: "docker0", rxBytes: 300, txBytes: 300},
	}
	rows := pickLinks(counters)
	if len(rows) != 2 {
		t.Fatalf("the loopback, the container plumbing, and the links that never moved a byte are no traffic: %+v", rows)
	}
	if rows[0].iface != "wlp69s0" || rows[1].iface != "tailscale0" {
		t.Fatalf("the busiest link leads: %+v", rows)
	}
	for i := 0; i < bandRows+2; i++ {
		counters = append(counters, linkCounters{iface: "eth" + string(rune('a'+i)), rxBytes: 1000 + uint64(i), txBytes: 1})
	}
	if n := len(pickLinks(counters)); n != bandRows {
		t.Fatalf("the band caps the links at %d, got %d", bandRows, n)
	}
}

func sample() *Sample {
	load := [3]float64{1, 2, 3}
	return &Sample{
		uptime: 100,
		load:   &load,
		mem:    &Memory{TotalKiB: 10, AvailableKiB: 5},
		cpu:    &counters{cores: 8, total: 100, idle: 50},
		links:  []linkCounters{{iface: "eno1", rxBytes: 1000, txBytes: 500}},
	}
}

func TestAFirstReadAnswersNoRate(t *testing.T) {
	s := sample()
	s.at = time.Unix(1000, 0)
	r := s.Report(nil)
	if r.CPU == nil || r.CPU.Cores != 8 || r.CPU.Percent != nil {
		t.Fatalf("the core count answers, the percent does not: %+v", r.CPU)
	}
	if len(r.Links) != 1 || r.Links[0].RxRate != nil || r.Links[0].RxBytes != 1000 {
		t.Fatalf("the bytes answered, the rate did not: %+v", r.Links)
	}
	if r.Load == nil || r.Memory == nil || r.UptimeS != 100 {
		t.Fatalf("the absolutes ride the first read: %+v", r)
	}
}

func TestTheSecondReadAnswersTheRates(t *testing.T) {
	prev := sample()
	prev.at = time.Unix(1000, 0)
	cur := sample()
	cur.at = prev.at.Add(2 * time.Second)
	cur.cpu.total, cur.cpu.idle = 200, 100
	cur.links[0].rxBytes, cur.links[0].txBytes = 2000, 500
	r := cur.Report(prev)
	f64(t, r.CPU.Percent, 50)
	f64(t, r.Links[0].RxRate, 500)
	f64(t, r.Links[0].TxRate, 0)
	cur.links[0].rxBytes = 100
	r = cur.Report(prev)
	if r.Links[0].RxRate != nil {
		t.Fatalf("a counter that fell answers no rate: %v", *r.Links[0].RxRate)
	}
}

func TestAnEmptySampleMarshalsToNothing(t *testing.T) {
	body, err := json.Marshal((&Sample{}).Report(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "{}" {
		t.Fatalf("a box with no source renders no rows: want {}, got %s", body)
	}
}

func TestALiveReadAnswersTheWorld(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the vitals are read from /proc and /sys")
	}
	s := Read(context.Background())
	if s.uptime <= 0 {
		t.Fatalf("the box has been up: %v", s.uptime)
	}
	if s.load == nil {
		t.Fatal("the kernel states a load")
	}
	if s.cpu == nil || s.cpu.cores <= 0 {
		t.Fatalf("the box has cores: %+v", s.cpu)
	}
	time.Sleep(50 * time.Millisecond)
	prev := s
	cur := Read(context.Background())
	r := cur.Report(prev)
	if r.CPU == nil || r.CPU.Percent == nil {
		t.Fatalf("two reads answer a busy share: %+v", r.CPU)
	}
	if *r.CPU.Percent < 0 || *r.CPU.Percent > 100 {
		t.Fatalf("the percent is a share: %v", *r.CPU.Percent)
	}
}

func TestTheGPUsComeFromTheWorldOrNowhere(t *testing.T) {
	s := Read(context.Background())
	for _, g := range s.gpus {
		if g.Index < 0 {
			t.Fatalf("a card without an index: %+v", g)
		}
		if g.TempC != nil && math.IsNaN(*g.TempC) {
			t.Fatalf("a card's temperature is a number: %+v", g)
		}
	}
}
