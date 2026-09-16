package loop

import (
	"context"
	"fmt"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
)

const defaultParallel = 8

type outcome struct {
	content string
	err     error
	dur     time.Duration
}

type batch struct {
	ctx        context.Context
	exec       core.ToolExec
	calls      []core.ToolCall
	concurrent func(core.ToolCall) bool
	sem        chan struct{}
	post       func(x int, out outcome)
	dispatched int
}

func newBatch(ctx context.Context, exec core.ToolExec, calls []core.ToolCall, concurrent func(core.ToolCall) bool, parallel int, post func(x int, out outcome)) *batch {
	b := &batch{ctx: ctx, exec: exec, calls: calls, concurrent: concurrent, post: post}
	if concurrent != nil {
		if parallel <= 0 {
			parallel = defaultParallel
		}
		b.sem = make(chan struct{}, parallel)
	}
	return b
}

// dispatch launches call i if it has not started and returns the
// exclusive end of the wave it launched: i+1 for a serial call, the end
// of the consecutive concurrent run for a wave. The caller owns the
// ToolStart notification for every call in [i, end), so a wave's starts
// all land on the frontend before any of the wave's results can.
func (b *batch) dispatch(i int) int {
	if i < b.dispatched {
		return b.dispatched
	}
	if b.concurrent != nil && b.concurrent(b.calls[i]) {
		j := i
		for j < len(b.calls) && b.concurrent(b.calls[j]) {
			j++
		}
		for x := i; x < j; x++ {
			go b.run(x)
		}
		b.dispatched = j
		return j
	}
	go b.run(i)
	b.dispatched = i + 1
	return i + 1
}

func (b *batch) run(x int) {
	if b.sem != nil {
		b.sem <- struct{}{}
		defer func() { <-b.sem }()
	}
	start := time.Now()
	var content string
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("tool panic: %v", r)
			}
		}()
		content, err = b.exec(b.ctx, b.calls[x])
	}()
	b.post(x, outcome{content: content, err: err, dur: time.Since(start)})
}
