package paths

import (
	"context"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
)

var Fields = []string{"path", "root", "project", "dir", "directory", "file", "target", "dest", "destination", "workspace"}

func Expand(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	rest := p[1:]
	name := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		name, rest = rest[:i], rest[i:]
	} else {
		rest = ""
	}
	home := ""
	if name == "" {
		home, _ = os.UserHomeDir()
	} else if u, err := user.Lookup(name); err == nil {
		home = u.HomeDir
	}
	if home == "" {
		return p
	}
	return filepath.Join(home, rest)
}

func Middleware(rec ...decision.Recorder) core.ToolMiddleware {
	var record decision.Recorder
	if len(rec) > 0 {
		record = rec[0]
	}
	return core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec {
		return func(ctx context.Context, call core.ToolCall) (string, error) {
			onExpand := func(field, raw, expanded string) {
				record.Record(ctx, decision.Final{
					Site:     decision.SitePaths,
					State:    raw,
					Question: decision.Binary("expand", "expand ~ in "+field+"?"),
					Answer:   expanded,
					Decider:  decision.SitePaths,
				})
			}
			if record == nil {
				onExpand = nil
			}
			if args, changed := rewrite(call.Args, onExpand); changed {
				call.Args = args
			}
			return next(ctx, call)
		}
	})
}

func Rewrite(args json.RawMessage) (json.RawMessage, bool) {
	return rewrite(args, nil)
}

func rewrite(args json.RawMessage, onExpand func(field, raw, expanded string)) (json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil || m == nil {
		return args, false
	}
	changed := false
	for _, f := range Fields {
		raw, ok := m[f]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		if e := Expand(s); e != s {
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			m[f] = b
			changed = true
			if onExpand != nil {
				onExpand(f, s, e)
			}
		}
	}
	if !changed {
		return args, false
	}
	out, err := json.Marshal(m)
	if err != nil {
		return args, false
	}
	return out, true
}
