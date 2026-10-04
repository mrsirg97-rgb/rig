package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/models"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
)

type dbSink struct {
	db    store.DB
	scope string
}

func (s *dbSink) ProposePending(ctx context.Context, p decision.Pending, a decision.Answer) error {
	scope := p.Scope
	if scope == "" {
		scope = s.scope
	}
	_, err := decisionstore.Propose(ctx, s.db, decisionstore.ProposeInput{
		Scope: scope, Session: p.Session, Site: p.Site, State: p.State,
		Question: p.Question, Answer: a.Value, Confidence: a.Confidence, Decider: a.Decider,
	})
	return err
}

type dbReviews struct {
	db store.DB
}

func (r *dbReviews) Pending(ctx context.Context) ([]decision.ReviewRow, error) {
	rows, err := decisionstore.Pending(ctx, r.db)
	if err != nil {
		return nil, err
	}
	out := make([]decision.ReviewRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, decision.ReviewRow{
			ID: row.ID, Site: row.Site, State: row.State, Question: row.Question,
			Answer: row.Answer, Confidence: row.Confidence, Decider: row.Decider,
		})
	}
	return out, nil
}

func (r *dbReviews) Settle(ctx context.Context, id int64, approved bool, reviewer, answer string) error {
	return decisionstore.Settle(ctx, r.db, decisionstore.SettleInput{
		ID: id, Approved: approved, Reviewer: reviewer, ReviewerAnswer: answer,
	})
}

func (r *root) reviewFire(home string, db store.DB, swapURL, self, cfgDir, sandbox string, sandboxBinds []string) decision.Fire {
	return func(ctx context.Context, prompt string, voice broadcast.Member) (string, error) {
		delegate := r.delegate
		if delegate == nil {
			delegate = sched.Delegate
		}
		res, err := delegate(sched.DelegateInput{
			DB:            db,
			Home:          home,
			Session:       core.NewSession().ID,
			Cwd:           r.cwd,
			Task:          prompt,
			Model:         "",
			WorkerSession: core.NewSession().ID,
			DefaultModel:  r.activeID,
			Models:        func() models.Table { return r.runtime },
			Fetch:         sched.RealFetch(0),
			Spawn:         sched.RealSpawn,
			WorkerCmd:     []string{self},
			SwapURL:       swapURL,
			Timeout:       time.Duration(-1),
			RigHome:       cfgDir,
			StateDir:      filepath.Join(cfgDir, "sessions"),
			Sandbox:       sandbox,
			SandboxBinds:  sandboxBinds,
			Bare:          true,
			Allow:         []string{"verdict"},
			Member:        voice,
			SpawnCtx:      ctx,
		})
		if err != nil {
			return "", err
		}
		return res.Model, res.FireError(home)
	}
}
