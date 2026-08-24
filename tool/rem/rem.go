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
		"project": {
			"type": "string",
			"description": "The repo a fact belongs to when you did not start in it: a path, resolved through store/scope (worktree-safe; ~ expands at the boundary)"
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

const description = "memory across sessions: learn commits a fact or constraint (idempotent), recall finds past " +
	"solutions by intent (project scope first, global fill), reflect stores a distilled memory with its source, " +
	"prune removes, reduces, or consolidates. Guidelines: recall before re-deriving a project fact; learn " +
	"deliberately what the next session should not re-derive; supersede by id when the code disagrees; name " +
	"project when the fact belongs to a repo you did not start in. Reply: the hits with their ids (mN) and " +
	"strength, or the written row; copy ids, never invent; no query is a browse."

type adapter struct{ db store.DB }

func New(db store.DB) core.Tool { return adapter{db: db} }

func (a adapter) Name() string        { return "rem" }
func (a adapter) Description() string { return description }
func (a adapter) Schema() json.RawMessage {
	return json.RawMessage(schemaJSON)
}

type given struct {
	Action            string   `json:"action"`
	Content           *string  `json:"content"`
	Query             *string  `json:"query"`
	Source            *string  `json:"source"`
	Kind              *string  `json:"kind"`
	Importance        *float64 `json:"importance"`
	Scope             *string  `json:"scope"`
	Project           *string  `json:"project"`
	K                 *int     `json:"k"`
	Verb              *string  `json:"verb"`
	IDs               []any    `json:"ids"`
	OlderThanDays     *int     `json:"older_than_days"`
	Supersedes        any      `json:"supersedes"`
	IncludeSuperseded *bool    `json:"include_superseded"`
}

func (a adapter) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var g given
	if err := json.Unmarshal(args, &g); err != nil {
		return "", fmt.Errorf("rem: %v", err)
	}
	cwd := ""
	if g.Project != nil && *g.Project != "" {
		cwd = *g.Project
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("rem: %v", err)
		}
		cwd = wd
	}
	if g.Project != nil && *g.Project != "" && g.Scope != nil && *g.Scope == "global" {
		return "", fmt.Errorf("rem: project + scope:global: a global memory has no project")
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
	return 0
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
