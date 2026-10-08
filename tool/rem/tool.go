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
	Learn(ctx context.Context, scope string, in LearnInput) (string, error)
	Recall(ctx context.Context, scope string, in RecallInput) (string, error)
	Reflect(ctx context.Context, scope string, in ReflectInput) (string, error)
	Prune(ctx context.Context, scope string, in PruneInput) (string, error)
}

type LearnInput struct {
	Content    string
	Kind       string
	Importance *float64
	Source     string
	Supersedes []int64
}

type RecallInput struct {
	Query             string
	Kind              string
	K                 *int
	IncludeSuperseded bool
}

type ReflectInput struct {
	Content    string
	Importance *float64
	Source     string
}

type PruneInput struct {
	Verb          string
	Kind          string
	IDs           []int64
	OlderThanDays *int
	Importance    *float64
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
		return a.Learn(ctx, scope, LearnInput{
			Content: text(g.Content), Kind: text(g.Kind), Importance: g.Importance, Source: text(g.Source), Supersedes: supersedes,
		})
	case "recall":
		return a.Recall(ctx, scope, RecallInput{
			Query: text(g.Query), Kind: text(g.Kind), K: g.K, IncludeSuperseded: g.IncludeSuperseded != nil && *g.IncludeSuperseded,
		})
	case "reflect":
		return a.Reflect(ctx, scope, ReflectInput{Content: text(g.Content), Importance: g.Importance, Source: text(g.Source)})
	case "prune":
		ids, err := idsOf(g.IDs)
		if err != nil {
			return "", err
		}
		return a.Prune(ctx, scope, PruneInput{
			Verb: text(g.Verb), Kind: text(g.Kind), IDs: ids, OlderThanDays: g.OlderThanDays, Importance: g.Importance,
		})
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

func (a adapter) Learn(ctx context.Context, scope string, in LearnInput) (string, error) {
	cwd, global, err := scopeOf(scope)
	if err != nil {
		return "", err
	}
	if in.Content == "" {
		return "", fmt.Errorf("rem: action 'learn' requires content")
	}
	weight, weightSet, err := importanceOf(in.Importance)
	if err != nil {
		return "", err
	}
	kind := in.Kind
	if kind == "" {
		kind = "fact"
	}
	reply, _, _, err := remstore.Learn(ctx, a.db, cwd, remstore.LearnInput{
		Content:       in.Content,
		Kind:          kind,
		Importance:    weight,
		ImportanceSet: weightSet,
		Scope:         internalScope(global),
		Source:        attributedSource(in.Source, ctx),
		Supersedes:    in.Supersedes,
	})
	return reply, err
}

func (a adapter) Recall(ctx context.Context, scope string, in RecallInput) (string, error) {
	cwd, global, err := scopeOf(scope)
	if err != nil {
		return "", err
	}
	if in.K != nil && (*in.K < 1 || *in.K > 50) {
		return "", fmt.Errorf("rem: k must be within 1..50, got %d", *in.K)
	}
	var found int
	if in.K != nil {
		found = *in.K
	}
	reply, _, err := remstore.Recall(ctx, a.db, cwd, remstore.RecallInput{
		Query:             in.Query,
		Scope:             internalScope(global),
		Kind:              in.Kind,
		K:                 found,
		IncludeSuperseded: in.IncludeSuperseded,
	})
	return reply, err
}

func (a adapter) Reflect(ctx context.Context, scope string, in ReflectInput) (string, error) {
	cwd, global, err := scopeOf(scope)
	if err != nil {
		return "", err
	}
	if in.Content == "" {
		return "", fmt.Errorf("rem: action 'reflect' requires content")
	}
	weight, weightSet, err := importanceOf(in.Importance)
	if err != nil {
		return "", err
	}
	if !weightSet {
		weight = 0.3
	}
	reply, _, _, err := remstore.Reflect(ctx, a.db, cwd, remstore.ReflectInput{
		Content:       in.Content,
		Importance:    weight,
		ImportanceSet: weightSet,
		Scope:         internalScope(global),
		Source:        attributedSource(in.Source, ctx),
	})
	return reply, err
}

func (a adapter) Prune(ctx context.Context, scope string, in PruneInput) (string, error) {
	cwd, global, err := scopeOf(scope)
	if err != nil {
		return "", err
	}
	switch in.Verb {
	case "":

	case "consolidate", "remove", "reduce":
	default:
		return "", fmt.Errorf("rem: verb must be remove, reduce, or consolidate, got '%s'", in.Verb)
	}
	var weight *float64
	if in.Importance != nil {
		v, _, err := importanceOf(in.Importance)
		if err != nil {
			return "", err
		}
		weight = &v
	}
	older := 0
	if in.OlderThanDays != nil {
		older = *in.OlderThanDays
		if older < 1 {
			return "", fmt.Errorf("rem: older_than_days must be at least 1, got %d", older)
		}
	}
	reply, _, err := remstore.Prune(ctx, a.db, cwd, remstore.PruneInput{
		Verb:          in.Verb,
		IDs:           in.IDs,
		Scope:         internalScope(global),
		Kind:          in.Kind,
		OlderThanDays: older,
		Importance:    weight,
	})
	return reply, err
}
