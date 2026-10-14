package main

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/rig/v3"
	"github.com/mrsirg97-rgb/rig/v3/store"
)

func TestAStatusPollOverlapsADialChangeWithoutTearing(t *testing.T) {
	engine := evt.NewEngine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go engine.Start(ctx)
	t.Cleanup(engine.Stop)
	r := testRoot(nullFrontend{})
	r.session = nil
	r.engine = engine
	status := webStatus(r, store.DB{})
	const changes = 256
	first := make(chan struct{})
	flip := make(chan struct{})
	go func() {
		defer close(flip)
		level := ""
		for i := 0; i < changes; i++ {
			level = "high"
			if i%2 == 1 {
				level = ""
			}
			lvl := level
			engine.Add(evt.Func(func(context.Context) { r.effort = lvl }), rig.PriorityWrite)
		}
	}()
	go func() {
		status(context.Background())
		close(first)
		for i := 0; i < changes*4; i++ {
			status(context.Background())
		}
	}()
	<-first
	if got := status(context.Background()).Effort; got != "" && got != "high" {
		t.Fatalf("a poll must see a whole dial, got %q", got)
	}
	<-flip
	if got := status(context.Background()).Effort; got != "" {
		t.Fatalf("after the last change the poll must see the resting dial, got %q", got)
	}
}
