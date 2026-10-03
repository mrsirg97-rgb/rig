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
	"os"
)

type adapter struct {
	tool.Definition
	db    store.DB
	graph *graph.Queue
}

func New(db store.DB, g *graph.Queue) core.Tool {
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
	Project           *string  `json:"project"`
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
	case "index":
		return a.graph.IndexProject(ctx, cwd)
	case "pack":
		if g.Target == nil || *g.Target == "" {
			return "", fmt.Errorf("rem: action 'pack' requires target")
		}
		return a.graph.Pack(ctx, cwd, *g.Target)
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
