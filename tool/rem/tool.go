package rem

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/graph"
	remstore "github.com/mrsirg97-rgb/rig/v2/store/rem"
	"github.com/mrsirg97-rgb/rig/v2/tool"
)

type Rem interface {
	tool.Definition
	Exec(ctx context.Context, args json.RawMessage) (string, error)

	Index(ctx context.Context, scope string) (string, error)
	Pack(ctx context.Context, scope, target string) (string, error)
	Learn(ctx context.Context, scope, content, kind string, importance *float64, source string, supersedes []int64) (string, error)
	Recall(ctx context.Context, scope, query, kind string, k *int, includeSuperseded bool) (string, error)
	Reflect(ctx context.Context, scope, content string, importance *float64, source string) (string, error)
	Prune(ctx context.Context, scope, verb, kind string, ids []int64, olderThanDays *int, importance *float64) (string, error)
}

type adapter struct {
	tool.Definition
	db    store.DB
	graph *graph.Queue
}

func New(db store.DB, g *graph.Queue) Rem {
	return adapter{Definition: tool.Def("rem"), db: db, graph: g}
}

func Guide() core.ToolMiddleware {
	return guideLink{}
}

type guideLink struct{}

func (guideLink) Wrap(next core.ToolExec) core.ToolExec { return next }

func (guideLink) Guidelines() string {
	return "in a mapped project, pack the task before reading files for it."
}

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
	Target            *string  `json:"target"`
}

func (a adapter) Exec(ctx context.Context, args json.RawMessage) (string, error) {
	var g given
	if err := json.Unmarshal(args, &g); err != nil {
		return "", fmt.Errorf("rem: %v", err)
	}
	scope := text(g.Scope)
	switch g.Action {
	case "":
		return "", fmt.Errorf("rem: action required")
	case "index":
		return a.Index(ctx, scope)
	case "pack":
		return a.Pack(ctx, scope, text(g.Target))
	case "learn":
		supersedes, err := supersedesOf(g.Supersedes)
		if err != nil {
			return "", err
		}
		return a.Learn(ctx, scope, text(g.Content), text(g.Kind), g.Importance, text(g.Source), supersedes)
	case "recall":
		return a.Recall(ctx, scope, text(g.Query), text(g.Kind), g.K, g.IncludeSuperseded != nil && *g.IncludeSuperseded)
	case "reflect":
		return a.Reflect(ctx, scope, text(g.Content), g.Importance, text(g.Source))
	case "prune":
		ids, err := idsOf(g.IDs)
		if err != nil {
			return "", err
		}
		return a.Prune(ctx, scope, text(g.Verb), text(g.Kind), ids, g.OlderThanDays, g.Importance)
	default:
		return "", fmt.Errorf("rem: action '%s' not implemented", g.Action)
	}
}

func text(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (a adapter) Index(ctx context.Context, scope string) (string, error) {
	cwd, global, err := scopeOf(scope)
	if err != nil {
		return "", err
	}
	if global {
		return "", fmt.Errorf("rem: index: global has no map; a map needs a directory")
	}
	return a.graph.IndexProject(ctx, cwd)
}

func (a adapter) Pack(ctx context.Context, scope, target string) (string, error) {
	cwd, global, err := scopeOf(scope)
	if err != nil {
		return "", err
	}
	if global {
		return "", fmt.Errorf("rem: pack: global has no map; a map needs a directory")
	}
	if target == "" {
		return "", fmt.Errorf("rem: action 'pack' requires target")
	}
	return a.graph.Pack(ctx, cwd, target)
}

func (a adapter) Learn(ctx context.Context, scope, content, kind string, importance *float64, source string, supersedes []int64) (string, error) {
	cwd, global, err := scopeOf(scope)
	if err != nil {
		return "", err
	}
	if content == "" {
		return "", fmt.Errorf("rem: action 'learn' requires content")
	}
	weight, weightSet, err := importanceOf(importance)
	if err != nil {
		return "", err
	}
	if kind == "" {
		kind = "fact"
	}
	reply, _, _, err := remstore.Learn(ctx, a.db, cwd, remstore.LearnInput{
		Content:       content,
		Kind:          kind,
		Importance:    weight,
		ImportanceSet: weightSet,
		Scope:         internalScope(global),
		Source:        attributedSource(source, ctx),
		Supersedes:    supersedes,
	})
	return reply, err
}

func (a adapter) Recall(ctx context.Context, scope, query, kind string, k *int, includeSuperseded bool) (string, error) {
	cwd, global, err := scopeOf(scope)
	if err != nil {
		return "", err
	}
	if k != nil && (*k < 1 || *k > 50) {
		return "", fmt.Errorf("rem: k must be within 1..50, got %d", *k)
	}
	var found int
	if k != nil {
		found = *k
	}
	reply, _, err := remstore.Recall(ctx, a.db, cwd, remstore.RecallInput{
		Query:             query,
		Scope:             internalScope(global),
		Kind:              kind,
		K:                 found,
		IncludeSuperseded: includeSuperseded,
	})
	return reply, err
}

func (a adapter) Reflect(ctx context.Context, scope, content string, importance *float64, source string) (string, error) {
	cwd, global, err := scopeOf(scope)
	if err != nil {
		return "", err
	}
	if content == "" {
		return "", fmt.Errorf("rem: action 'reflect' requires content")
	}
	weight, weightSet, err := importanceOf(importance)
	if err != nil {
		return "", err
	}
	if !weightSet {
		weight = 0.3
	}
	reply, _, _, err := remstore.Reflect(ctx, a.db, cwd, remstore.ReflectInput{
		Content:       content,
		Importance:    weight,
		ImportanceSet: weightSet,
		Scope:         internalScope(global),
		Source:        attributedSource(source, ctx),
	})
	return reply, err
}

func (a adapter) Prune(ctx context.Context, scope, verb, kind string, ids []int64, olderThanDays *int, importance *float64) (string, error) {
	cwd, global, err := scopeOf(scope)
	if err != nil {
		return "", err
	}
	switch verb {
	case "":

	case "consolidate", "remove", "reduce":
	default:
		return "", fmt.Errorf("rem: verb must be remove, reduce, or consolidate, got '%s'", verb)
	}
	var weight *float64
	if importance != nil {
		v, _, err := importanceOf(importance)
		if err != nil {
			return "", err
		}
		weight = &v
	}
	older := 0
	if olderThanDays != nil {
		older = *olderThanDays
		if older < 1 {
			return "", fmt.Errorf("rem: older_than_days must be at least 1, got %d", older)
		}
	}
	reply, _, err := remstore.Prune(ctx, a.db, cwd, remstore.PruneInput{
		Verb:          verb,
		IDs:           ids,
		Scope:         internalScope(global),
		Kind:          kind,
		OlderThanDays: older,
		Importance:    weight,
	})
	return reply, err
}
