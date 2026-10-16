package room

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/config"
)

const (
	PriorityOutside = 0
	PriorityChild   = 20
	PriorityFleet   = 30
	PriorityReturn  = 70
	PriorityTurn    = 90
)

const joinAttempts = 64

const meshPeriod = time.Duration(config.DefaultMeshPeriod) * time.Millisecond

const JobStatusPostWait = 2 * meshPeriod

var procStat = "/proc/self/stat"

const sidecarExt = ".version"

var codecOf = Codec

type Member interface {
	Id() int64
	Seated() bool
	Close()
	Gone() <-chan struct{}
	Ask(f func()) bool
	Claim(key int64, priority int, talk int64, posted func(mint broadcast.Mint, admitted bool))
	Release(claim broadcast.Mint, priority int)
	Act(payload evt.Signal, key int64, priority int, posted func(admitted bool))
	Holders(key int64) []broadcast.Operation
	Waiting(key int64) []broadcast.Operation
	Live() []int64
	Admits(version string) bool
}

type member struct {
	id       int64
	engine   evt.Engine
	claim    broadcast.Claimant
	home     string
	version  string
	listener net.Listener
	mu       sync.Mutex
	trs      []broadcast.Transport
	gone     <-chan struct{}
	cancel   context.CancelFunc
}

func New(home string, version string, places func(int64) int, effects func(self int64) broadcast.Effects, told func(evt.Notice)) (Member, error) {
	dir := filepath.Join(home, "room")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	engine := evt.NewEngine()
	ctx, cancel := context.WithCancel(context.Background())
	seed, err := identity()
	if err != nil {
		cancel()
		return nil, err
	}
	var listener net.Listener
	var members []int64
	var id int64
	joined := false
	enc := codecOf()
	for i := 0; i < joinAttempts; i++ {
		id = broadcast.MemberOf([]byte(seed + " " + strconv.Itoa(i)))
		listener, err = broadcast.Listen(dir, id)
		if err != nil {
			if !errors.Is(err, syscall.EADDRINUSE) {
				cancel()
				return nil, err
			}
			continue
		}
		members, err = broadcast.Scan(dir)
		if err != nil {
			listener.Close()
			cancel()
			return nil, err
		}
		if mine(id, members) == 1 {
			joined = true
			break
		}
		listener.Close()
	}
	if !joined {
		cancel()
		return nil, fmt.Errorf("room: no free id in %s after %d attempts", dir, joinAttempts)
	}
	if version != "" {
		if err := os.WriteFile(filepath.Join(dir, strconv.FormatInt(id, 10)+sidecarExt), []byte(version), 0o644); err != nil {
			listener.Close()
			cancel()
			return nil, err
		}
	}
	var extra broadcast.Effects
	if effects != nil {
		extra = effects(id)
	}
	claim := broadcast.NewClaimant(engine, engine, id, broadcast.NewMinter(id, broadcast.MachineClock()), broadcast.NewFold(places, extra), PriorityFleet, told)
	m := &member{id: id, engine: engine, claim: claim, home: home, version: version, listener: listener, gone: ctx.Done(), cancel: cancel}
	go engine.Start(ctx)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			tr := broadcast.NewPipeTransport(0, conn, enc)
			m.track(tr)
			claim.Accept(tr)
		}
	}()
	for _, peer := range members {
		if peer == id {
			continue
		}
		conn, err := broadcast.Dial(dir, peer)
		if err != nil {
			continue
		}
		tr := broadcast.NewPipeTransport(peer, conn, enc)
		m.track(tr)
		claim.Subscribe(tr)
	}
	claim.Settle()
	return m, nil
}

func mine(id int64, members []int64) int {
	n := 0
	for _, other := range members {
		if other == id {
			n++
		}
	}
	return n
}

func (m *member) Close() {
	m.cancel()
	m.listener.Close()
	m.mu.Lock()
	trs := m.trs
	m.trs = nil
	m.mu.Unlock()
	for _, tr := range trs {
		tr.Close()
	}
	if m.version != "" {
		os.Remove(filepath.Join(m.home, "room", strconv.FormatInt(m.id, 10)+sidecarExt))
	}
}

func (m *member) track(tr broadcast.Transport) {
	m.mu.Lock()
	m.trs = append(m.trs, tr)
	m.mu.Unlock()
}

func (m *member) Id() int64 { return m.id }

func (m *member) Gone() <-chan struct{} { return m.gone }

func (m *member) Ask(f func()) bool {
	done := make(chan struct{})
	m.engine.Add(evt.Func(func(context.Context) { f(); close(done) }), PriorityFleet)
	select {
	case <-done:
		return true
	case <-m.gone:
		return false
	}
}

func (m *member) Seated() bool {
	seated := false
	return m.Ask(func() { seated = m.claim.Seated() }) && seated
}

func (m *member) Claim(key int64, priority int, talk int64, posted func(mint broadcast.Mint, admitted bool)) {
	m.claim.Claim(key, priority, talk, posted)
}

func (m *member) Release(claim broadcast.Mint, priority int) { m.claim.Release(claim, priority) }

func (m *member) Act(payload evt.Signal, key int64, priority int, posted func(admitted bool)) {
	m.claim.Act(payload, key, priority, posted)
}

func (m *member) Holders(key int64) []broadcast.Operation {
	var out []broadcast.Operation
	if !m.Ask(func() { out = m.claim.Holders(key) }) {
		return nil
	}
	return out
}

func (m *member) Waiting(key int64) []broadcast.Operation {
	var out []broadcast.Operation
	if !m.Ask(func() { out = m.claim.Waiting(key) }) {
		return nil
	}
	return out
}

func (m *member) Live() []int64 {
	var out []int64
	if !m.Ask(func() { out = m.claim.Live() }) {
		return nil
	}
	return out
}

func (m *member) Admits(version string) bool {
	if version == "" {
		return false
	}
	var peers []int64
	if !m.Ask(func() { peers = m.claim.Live() }) {
		return false
	}
	for _, peer := range peers {
		if !declares(filepath.Join(m.home, "room", strconv.FormatInt(peer, 10)+sidecarExt), version) {
			return false
		}
	}
	return true
}

func declares(file, want string) bool {
	b, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	return versionAtLeast(strings.TrimSpace(string(b)), want)
}

func versionAtLeast(have, want string) bool {
	hp, wp := strings.Split(have, "."), strings.Split(want, ".")
	for i, part := range wp {
		w, err := strconv.Atoi(part)
		if err != nil {
			return false
		}
		h := 0
		if i < len(hp) {
			if h, err = strconv.Atoi(hp[i]); err != nil {
				return false
			}
		}
		if h != w {
			return h > w
		}
	}
	return true
}

func identity() (string, error) {
	boot, err := bootID()
	if err != nil {
		return "", err
	}
	start, err := startTime()
	if err != nil {
		return "", err
	}
	return boot + fmt.Sprintf(" %d %d", os.Getpid(), start), nil
}

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

func Loading(port int) bool {
	client := http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusServiceUnavailable
}

func Answers(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
