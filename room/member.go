package room

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
)

const (
	PriorityOutside = 0
	PriorityChild   = 20
	PriorityFleet   = 30
	PriorityReturn  = 70
	PriorityTurn    = 90
)

type Member interface {
	Id() int64
	Seated() bool
	Close()
	Claim(key int64, priority int, talk int64, posted func(mint broadcast.Mint, admitted bool))
	Release(claim broadcast.Mint, priority int)
	Act(payload evt.Signal, key int64, priority int, posted func(admitted bool))
	Holders(key int64) []broadcast.Operation
	Waiting(key int64) []broadcast.Operation
	Live() []int64
	Say(source, text string, level ...evt.Level)
}

type member struct {
	id       int64
	engine   evt.Engine
	claim    broadcast.Claimant
	home     string
	listener net.Listener
	mu       sync.Mutex
	trs      []broadcast.Transport
	cancel   context.CancelFunc
}

func New(home string, places func(int64) int, extra broadcast.Effects, told func(evt.Notice)) (Member, error) {
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
	for i := 0; ; i++ {
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
			break
		}
		listener.Close()
	}
	claim := broadcast.NewClaimant(engine, engine, id, broadcast.NewMinter(id, broadcast.MachineClock()), broadcast.NewFold(places, extra), PriorityFleet, told)
	m := &member{id: id, engine: engine, claim: claim, home: home, listener: listener, cancel: cancel}
	go engine.Start(ctx)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			tr := broadcast.NewPipeTransport(0, conn, Codec())
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
		tr := broadcast.NewPipeTransport(peer, conn, Codec())
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
}

func (m *member) track(tr broadcast.Transport) {
	m.mu.Lock()
	m.trs = append(m.trs, tr)
	m.mu.Unlock()
}

func (m *member) Id() int64 { return m.id }

func (m *member) ask(f func()) {
	done := make(chan struct{})
	m.engine.Add(evt.Func(func(context.Context) { f(); close(done) }), PriorityFleet)
	<-done
}

func (m *member) Seated() bool {
	seated := false
	m.ask(func() { seated = m.claim.Seated() })
	return seated
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
	m.ask(func() { out = m.claim.Holders(key) })
	return out
}

func (m *member) Waiting(key int64) []broadcast.Operation {
	var out []broadcast.Operation
	m.ask(func() { out = m.claim.Waiting(key) })
	return out
}

func (m *member) Live() []int64 {
	var out []int64
	m.ask(func() { out = m.claim.Live() })
	return out
}

func (m *member) Say(source, text string, level ...evt.Level) {
	n := evt.Notice{Source: source, Text: text}
	if len(level) > 0 {
		n.Level = level[0]
	}
	m.claim.Act(n, KeyOf("notice/"+source), PriorityFleet, func(bool) {})
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
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, nil
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

func Answers(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
