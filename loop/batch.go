package loop

import (
	"context"
	"fmt"
	"time"

	"github.com/mrsirg97-rgb/rig/v3"
	"github.com/mrsirg97-rgb/rig/v3/core"
)

type outcome struct {
	content string
	err     error
	dur     time.Duration
}

type batch struct {
	ctx        context.Context
	exec       core.ToolExec
	calls      []core.ToolCall
	priorities []int
	sem        chan struct{}
	submit     func(priority int, f func())
	post       func(x int, out outcome)
	dispatched int
}

func newBatch(ctx context.Context, exec core.ToolExec, calls []core.ToolCall, priority func(core.ToolCall) int, parallel int, post func(x int, out outcome), submit func(priority int, f func())) *batch {
	b := &batch{ctx: ctx, exec: exec, calls: calls, priorities: make([]int, len(calls)), submit: submit, post: post}
	for i := range calls {
		p := rig.PriorityWrite
		if priority != nil {
			p = priority(calls[i])
		}
		b.priorities[i] = p
	}
	b.sem = make(chan struct{}, parallel)
	return b
}

func (b *batch) dispatch(i int) int {
	if i < b.dispatched {
		return b.dispatched
	}
	for x := 0; x < len(b.calls); x++ {
		if b.priorities[x] >= rig.PriorityWrite {
			b.submit(b.priorities[x], func() {
				out := b.call(x)
				b.submit(b.priorities[x], func() { b.post(x, out) })
			})
			continue
		}
		b.submit(b.priorities[x], func() {
			go b.run(x)
		})
	}
	b.dispatched = len(b.calls)
	return len(b.calls)
}

func (b *batch) call(x int) outcome {
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
	return outcome{content: content, err: err, dur: time.Since(start)}
}

func (b *batch) run(x int) {
	b.sem <- struct{}{}
	defer func() { <-b.sem }()
	out := b.call(x)
	b.submit(b.priorities[x], func() { b.post(x, out) })
}
