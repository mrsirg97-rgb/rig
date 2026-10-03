package main

import (
	"context"
	"errors"
	"path/filepath"
	"time"

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

func (r *root) pendingReviewCount(ctx context.Context) (int, error) {
	if r.decRev == nil {
		return 0, errors.New("decide: no reviewer (headless workers and jobs never review)")
	}
	rows, err := r.decReviews.Pending(ctx)
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

func (r *root) reviewNow(ctx context.Context) (string, error) {
	if r.decRev == nil {
		return "", errors.New("decide: no reviewer (headless workers and jobs never review)")
	}
	go func() {
		summary, err := r.decRev.Drain(ctx)
		switch {
		case err != nil:
			r.notice("decision", "review: "+err.Error())
		case summary != "":
			r.notice("decision", "review: "+summary)
		}
	}()
	return "the review fire is running; the outcome arrives as a notice", nil
}

func (r *root) reviewFire(home string, db store.DB, swapURL, self, cfgDir, sandbox string, sandboxBinds []string) decision.Fire {
	return func(ctx context.Context, prompt string) (string, string, error) {
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
			NoTools:       true,
			SpawnCtx:      ctx,
		})
		if err != nil {
			return "", "", err
		}
		if ferr := res.FireError(home); ferr != nil {
			return "", res.Model, ferr
		}
		return res.Stdout, res.Model, nil
	}
}
