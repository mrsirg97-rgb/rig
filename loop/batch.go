package loop

import (
	"context"
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
	done       []chan outcome
	dispatched int
}

func newBatch(ctx context.Context, exec core.ToolExec, calls []core.ToolCall, concurrent func(core.ToolCall) bool, parallel int) *batch {
	b := &batch{ctx: ctx, exec: exec, calls: calls, concurrent: concurrent, done: make([]chan outcome, len(calls))}
	if concurrent != nil {
		if parallel <= 0 {
			parallel = defaultParallel
		}
		b.sem = make(chan struct{}, parallel)
	}
	return b
}

func (b *batch) result(i int) outcome {
	if i >= b.dispatched {
		if b.concurrent != nil && b.concurrent(b.calls[i]) {
			j := i
			for j < len(b.calls) && b.concurrent(b.calls[j]) {
				j++
			}
			for x := i; x < j; x++ {
				b.done[x] = make(chan outcome, 1)
				go b.run(x)
			}
			b.dispatched = j
		} else {
			b.done[i] = make(chan outcome, 1)
			b.run(i)
			b.dispatched = i + 1
		}
	}
	return <-b.done[i]
}

func (b *batch) run(x int) {
	if b.sem != nil {
		b.sem <- struct{}{}
		defer func() { <-b.sem }()
	}
	start := time.Now()
	content, err := b.exec(b.ctx, b.calls[x])
	b.done[x] <- outcome{content: content, err: err, dur: time.Since(start)}
}
