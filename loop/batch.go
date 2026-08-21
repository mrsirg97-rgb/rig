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

func (b *batch) dispatch(i int) {
	if i < b.dispatched {
		return
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
		return
	}
	go b.run(i)
	b.dispatched = i + 1
}

func (b *batch) run(x int) {
	if b.sem != nil {
		b.sem <- struct{}{}
		defer func() { <-b.sem }()
	}
	start := time.Now()
	content, err := b.exec(b.ctx, b.calls[x])
	b.post(x, outcome{content: content, err: err, dur: time.Since(start)})
}
