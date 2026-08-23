package paths

import (
	"context"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/core"
)

var Fields = []string{"path", "root", "cwd"}

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

func Middleware() core.ToolMiddleware {
	return core.ToolMiddlewareFunc(func(next core.ToolExec) core.ToolExec {
		return func(ctx context.Context, call core.ToolCall) (string, error) {
			if args, changed := Rewrite(call.Args); changed {
				call.Args = args
			}
			return next(ctx, call)
		}
	})
}

func Rewrite(args json.RawMessage) (json.RawMessage, bool) {
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
