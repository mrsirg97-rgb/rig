package guard

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mrsirg97-rgb/rig/core"
)

// cap is the result bound: every tool result is bounded before it reaches
// the transcript, in one place. An oversized result truncates to the head
// and the tail with the loud [TRUNCATED] marker naming the full size.
type cap struct {
	bytes int
}

func Cap(bytes int) core.ToolMiddleware {
	if bytes < 1 {
		bytes = 1
	}
	return &cap{bytes: bytes}
}

func (c *cap) Wrap(next core.ToolExec) core.ToolExec {
	return func(ctx context.Context, call core.ToolCall) (string, error) {
		content, err := next(ctx, call)
		if len(content) <= c.bytes {
			return content, err
		}
		return truncate(content, c.bytes), err
	}
}

func truncate(content string, cap int) string {
	n := len(content)
	marker := fmt.Sprintf("[TRUNCATED] full %d bytes, kept the head and the tail, the middle elided\nre-read a narrower range (read offset/limit)\n", n)
	avail := cap - len(marker)
	if avail < 0 {
		avail = 0
	}
	head := avail / 2
	tail := avail - head
	for head > 0 && !utf8.RuneStart(content[head]) {
		head--
	}
	for tail > 0 && !utf8.RuneStart(content[n-tail]) {
		tail--
	}
	var b strings.Builder
	b.WriteString(content[:head])
	b.WriteString(marker)
	b.WriteString(content[n-tail:])
	return b.String()
}
