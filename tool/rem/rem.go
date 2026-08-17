// Package rem adapts store/rem to the loop's tool surface: pane's
// description, schema shape, and action voices verbatim; runtime
// shape checks loud at execute; session attribution from the threaded
// ctx (the named deviation: memories.source defaults to the calling
// session id, anon when unthreaded, and accepts free text when the
// caller passes one); replies exactly as the store shapes them.
package rem

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store"
	remstore "github.com/mrsirg97-rgb/rig/store/rem"
)

// schemaJSON is pane's parameters shape: action required, the flat
// optional surface with pane's description strings.
const schemaJSON = `{
	"type": "object",
	"required": ["action"],
	"properties": {
		"action": {
			"enum": ["learn", "recall", "reflect", "prune"]
		},
		"content": {
			"type": "string",
			"description": "Memory content (learn/reflect)"
		},
		"query": {
			"type": "string",
			"description": "Recall intent; omit to browse"
		},
		"source": {
			"type": "string",
			"description": "Raw source log for provenance (reflect)"
		},
		"kind": {
			"type": "string",
			"description": "Free-form kind label; reuse consistently"
		},
		"importance": {
			"type": "number",
			"minimum": 0,
			"maximum": 1,
			"description": "0..1; strength starts here and decays"
		},
		"scope": {
			"enum": ["project", "global", "all"]
		},
		"k": {
			"type": "integer",
			"minimum": 1,
			"maximum": 50,
			"description": "Live-hit budget (recall)"
		},
		"verb": {
			"enum": ["remove", "reduce", "consolidate"]
		},
		"ids": {
			"type": "array",
			"items": { "type": "integer" },
			"description": "Memory ids to prune"
		},
		"older_than_days": {
			"type": "integer",
			"minimum": 1,
			"description": "Selection criterion for prune"
		},
		"supersedes": {
			"anyOf": [
				{ "type": "integer" },
				{ "type": "array", "items": { "type": "integer" } }
			]
		},
		"include_superseded": {
			"type": "boolean",
			"description": "Fill unused budget with superseded hits"
		}
	}
}`

// description is pane's voice verbatim: lowercase, terse.
const description = "Memory tool: learn commits facts and constraints idempotently; recall fetches past " +
	"solutions by intent (fuzzy/semantic search, project-scoped first with global fill); " +
	"reflect stores a distilled memory with its raw source; prune consolidates the strength " +
	"arithmetic or removes/reduces memories. Scopes: 'global' for general knowledge, default " +
	"project (cwd). Recall k caps live hits; no query = browse. Ids are minted mN; copy, " +
	"never invent."

// adapter is the surface over one opened hybrid store. The root resolves
// the store file and hands the constructor its db; this seam only
// consumes it.
type adapter struct{ db store.DB }

// New hands the opened store to the surface.
func New(db store.DB) core.Tool { return adapter{db: db} }

func (a adapter) Name() string        { return "rem" }
func (a adapter) Description() string { return description }
func (a adapter) Schema() json.RawMessage {
	return json.RawMessage(schemaJSON)
}

// given is one decoded call.
type given struct {
	Action            string   `json:"action"`
	Content           *string  `json:"content"`
	Query             *string  `json:"query"`
	Source            *string  `json:"source"`
	Kind              *string  `json:"kind"`
	Importance        *float64 `json:"importance"`
	Scope             *string  `json:"scope"`
	K                 *int     `json:"k"`
	Verb              *string  `json:"verb"`
	IDs               []any    `json:"ids"`
	OlderThanDays     *int     `json:"older_than_days"`
	Supersedes        any      `json:"supersedes"`
	IncludeSuperseded *bool    `json:"include_superseded"`
}

// Exec maps the call onto the store's verbs with the threaded session.
// Runtime shape voices are pane's; the store's own refusals pass
// through.
func (a adapter) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var g given
	if err := json.Unmarshal(args, &g); err != nil {
		return "", fmt.Errorf("rem: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("rem: %v", err)
	}
	switch g.Action {
	case "":
		return "", fmt.Errorf("rem: action required")
	case "learn":
		if err := scopeCheck(g.Scope); err != nil {
			return "", err
		}
		if g.Content == nil || *g.Content == "" {
			return "", fmt.Errorf("rem: action 'learn' requires content")
		}
		importance, importanceSet, err := importanceOf(g.Importance)
		if err != nil {
			return "", err
		}
		supersedes, err := supersedesOf(g.Supersedes)
		if err != nil {
			return "", err
		}
		kind := "fact"
		if g.Kind != nil && *g.Kind != "" {
			kind = *g.Kind
		}
		reply, _, _, err := remstore.Learn(ctx, a.db, cwd, remstore.LearnInput{
			Content:       *g.Content,
			Kind:          kind,
			Importance:    importance,
			ImportanceSet: importanceSet,
			Scope:         scopeOf(g.Scope),
			Source:        attributedSource(g.Source, ctx),
			Supersedes:    supersedes,
		})
		return reply, err
	case "recall":
		if err := scopeCheck(g.Scope); err != nil {
			return "", err
		}
		if g.K != nil && (*g.K < 1 || *g.K > 50) {
			return "", fmt.Errorf("rem: k must be within 1..50, got %d", *g.K)
		}
		var k int
		if g.K != nil {
			k = *g.K
		}
		scope := scopeOf(g.Scope)
		reply, _, err := remstore.Recall(ctx, a.db, cwd, remstore.RecallInput{
			Query:             queryOf(g.Query),
			Scope:             scope,
			Kind:              kindOf(g.Kind),
			K:                 k,
			IncludeSuperseded: g.IncludeSuperseded != nil && *g.IncludeSuperseded,
		})
		return reply, err
	case "reflect":
		if err := scopeCheck(g.Scope); err != nil {
			return "", err
		}
		if g.Content == nil || *g.Content == "" {
			return "", fmt.Errorf("rem: action 'reflect' requires content")
		}
		importance, importanceSet, err := importanceOf(g.Importance)
		if err != nil {
			return "", err
		}
		if !importanceSet {
			importance = 0.3
		}
		reply, _, _, err := remstore.Reflect(ctx, a.db, cwd, remstore.ReflectInput{
			Content:       *g.Content,
			Importance:    importance,
			ImportanceSet: importanceSet,
			Scope:         scopeOf(g.Scope),
			Source:        attributedSource(g.Source, ctx),
		})
		return reply, err
	case "prune":
		if err := scopeCheck(g.Scope); err != nil {
			return "", err
		}
		verb := ""
		if g.Verb != nil {
			verb = *g.Verb
		}
		switch verb {
		case "":
			// the store's execute-time voice
		case "consolidate", "remove", "reduce":
		default:
			return "", fmt.Errorf("rem: verb must be remove, reduce, or consolidate, got '%s'", verb)
		}
		ids, err := idsOf(g.IDs)
		if err != nil {
			return "", err
		}
		scope := scopeOf(g.Scope)
		var importance *float64
		if g.Importance != nil {
			v, _, err := importanceOf(g.Importance)
			if err != nil {
				return "", err
			}
			importance = &v
		}
		older := 0
		if g.OlderThanDays != nil {
			older = *g.OlderThanDays
			if older < 1 {
				return "", fmt.Errorf("rem: older_than_days must be at least 1, got %d", older)
			}
		}
		reply, _, err := remstore.Prune(ctx, a.db, cwd, remstore.PruneInput{
			Verb:          verb,
			IDs:           ids,
			Scope:         scope,
			Kind:          kindOf(g.Kind),
			OlderThanDays: older,
			Importance:    importance,
		})
		return reply, err
	default:
		return "", fmt.Errorf("rem: action '%s' not implemented", g.Action)
	}
}

// attributedSource is the named deviation: an explicit source rides
// verbatim; absent, the calling session id (anon when unthreaded).
func attributedSource(explicit *string, ctx context.Context) string {
	if explicit != nil && *explicit != "" {
		return *explicit
	}
	if s, ok := core.SessionFrom(ctx); ok && s != nil && s.ID != "" {
		return s.ID
	}
	return "anon"
}

func scopeOf(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

// scopeCheck: pane's scope enum, loud at execute — unknown scopes
// refuse rather than silently default.
func scopeCheck(s *string) error {
	if s == nil || *s == "" || *s == "project" || *s == "global" || *s == "all" {
		return nil
	}
	return fmt.Errorf("rem: scope must be project, global, or all, got '%s'", *s)
}

func kindOf(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

func queryOf(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

func recallK(k *int) int {
	if k != nil {
		return *k
	}
	return 0 // the store defaults to 10 and caps at 50
}

func importanceOf(v *float64) (float64, bool, error) {
	if v == nil {
		return 0.5, false, nil
	}
	if *v < 0 || *v > 1 {
		return 0, false, fmt.Errorf("rem: importance must be within 0..1, got %g", *v)
	}
	return *v, true, nil
}

func supersedesOf(v any) ([]int64, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case float64:
		if t != float64(int64(t)) || t < 1 {
			return nil, fmt.Errorf("rem: supersedes must be memory ids of at least 1, got %g", t)
		}
		return []int64{int64(t)}, nil
	case []any:
		var out []int64
		for _, e := range t {
			f, ok := e.(float64)
			if !ok || f != float64(int64(f)) || f < 1 {
				return nil, fmt.Errorf("rem: supersedes must be memory ids of at least 1")
			}
			out = append(out, int64(f))
		}
		return out, nil
	}
	return nil, fmt.Errorf("rem: supersedes must be a memory id or a list of ids")
}

func idsOf(v []any) ([]int64, error) {
	if v == nil {
		return nil, nil
	}
	var out []int64
	for _, e := range v {
		f, ok := e.(float64)
		if !ok || f != float64(int64(f)) || f < 1 {
			return nil, fmt.Errorf("rem: ids must be memory ids of at least 1")
		}
		out = append(out, int64(f))
	}
	return out, nil
}
