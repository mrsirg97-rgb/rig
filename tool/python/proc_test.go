package python

import (
	"strings"
	"testing"
	"time"
)

func TestAReadLineOverTheCeilingRefusesAndDropsTheRest(t *testing.T) {
	p := &proc{readDone: make(chan struct{}), pending: map[string]*pendingCall{}}
	call := &pendingCall{ch: make(chan Reply, 1)}
	p.mu.Lock()
	p.pending["3"] = call
	p.mu.Unlock()
	over := `{"id":"2","out":"` + strings.Repeat("x", maxLine+1) + `"}` + "\n" + `{"id":"3","ok":true}` + "\n"
	go p.readLoop(strings.NewReader(over))
	select {
	case got := <-call.ch:
		t.Fatalf("a line over the ceiling must refuse, not continue: %+v", got)
	case <-time.After(200 * time.Millisecond):
	}
	select {
	case <-p.readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("an over-ceiling line must tear the read loop down")
	}
}

func TestAReadLineAtTheCeilingStillDelivers(t *testing.T) {
	p := &proc{readDone: make(chan struct{}), pending: map[string]*pendingCall{}}
	call := &pendingCall{ch: make(chan Reply, 1)}
	p.mu.Lock()
	p.pending["1"] = call
	p.mu.Unlock()
	line := `{"id":"1","ok":true,"out":"` + strings.Repeat("x", maxLine-64) + `"}` + "\n"
	go p.readLoop(strings.NewReader(line))
	select {
	case got := <-call.ch:
		if got.ID == nil || *got.ID != "1" {
			t.Fatalf("a reply at the ceiling delivers: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the reply never delivered")
	}
}
