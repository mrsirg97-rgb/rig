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
	PriorityTurn    = 90
)

const joinAttempts = 64

const meshPeriod = time.Duration(config.DefaultMeshPeriod) * time.Millisecond

const JobStatusPostWait = 2 * meshPeriod

const acceptBackoff = meshPeriod

var acceptFrom = func(l net.Listener) (net.Conn, error) { return l.Accept() }

const sidecarExt = ".version"

var codecOf = Codec

type Member interface {
	Id() int64
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
	closed   bool
	gone     <-chan struct{}
	cancel   context.CancelFunc
}

func New(home string, version string, places func(int64) int, effects func(self int64) broadcast.Effects, told func(evt.Notice)) (Member, error) {
	dir := roomDir(home)
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
			if !errors.Is(err, syscall.EADDRINUSE) && !errors.Is(err, syscall.EEXIST) {
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
			conn, err := acceptFrom(listener)
			if err != nil {
				if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
					return
				}
				if told != nil {
					told(evt.Notice{Source: "room", Level: evt.LevelError,
						Text: fmt.Sprintf("member %d: accept failed, retrying after %s: %v", id, acceptBackoff, err)})
				}
				select {
				case <-time.After(acceptBackoff):
				case <-ctx.Done():
					return
				}
				continue
			}
			if ctx.Err() != nil {
				conn.Close()
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

func roomDir(home string) string { return filepath.Join(home, "room") }

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
	m.closed = true
	trs := m.trs
	m.trs = nil
	m.mu.Unlock()
	for _, tr := range trs {
		tr.Close()
	}
	if m.version != "" {
		os.Remove(filepath.Join(roomDir(m.home), strconv.FormatInt(m.id, 10)+sidecarExt))
	}
}

func (m *member) track(tr broadcast.Transport) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		tr.Close()
		return
	}
	m.trs = append(m.trs, tr)
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

func (m *member) Claim(key int64, priority int, talk int64, posted func(mint broadcast.Mint, admitted bool)) {
	m.claim.Claim(key, priority, talk, posted)
}

func (m *member) Release(claim broadcast.Mint, priority int) { m.claim.Release(claim, priority) }

func (m *member) Act(payload evt.Signal, key int64, priority int, posted func(admitted bool)) {
	m.claim.Act(payload, key, priority, posted)
}

func (m *member) Holders(key int64) []broadcast.Operation {
	out, _ := ask(m, func() []broadcast.Operation { return m.claim.Holders(key) })
	return out
}

func (m *member) Waiting(key int64) []broadcast.Operation {
	out, _ := ask(m, func() []broadcast.Operation { return m.claim.Waiting(key) })
	return out
}

func (m *member) Live() []int64 {
	out, _ := ask(m, func() []int64 { return m.claim.Live() })
	return out
}

func ask[T any](m *member, f func() T) (T, bool) {
	done := make(chan T, 1)
	if !m.Ask(func() { done <- f() }) {
		var zero T
		return zero, false
	}
	return <-done, true
}

func (m *member) Admits(version string) bool {
	if version == "" {
		return false
	}
	peers, ok := ask(m, func() []int64 { return m.claim.Live() })
	if !ok {
		return false
	}
	for _, peer := range peers {
		if !declares(filepath.Join(roomDir(m.home), strconv.FormatInt(peer, 10)+sidecarExt), version) {
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
