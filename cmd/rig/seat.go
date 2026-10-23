package main

import (
	"sync/atomic"

	"github.com/mrsirg97-rgb/rig/v3/core"
)

type seatParts struct {
	provider core.Provider
	frontend core.Frontend
	policy   core.ContextPolicy
	session  *core.Session
}

type seat struct {
	parts atomic.Pointer[seatParts]
}

func newSeat(provider core.Provider, frontend core.Frontend, policy core.ContextPolicy, session *core.Session) *seat {
	if session == nil {
		session = core.NewSession()
	}
	s := &seat{}
	s.parts.Store(&seatParts{provider: provider, frontend: frontend, policy: policy, session: session})
	return s
}

func (s *seat) Provider() core.Provider    { return s.parts.Load().provider }
func (s *seat) Frontend() core.Frontend    { return s.parts.Load().frontend }
func (s *seat) Policy() core.ContextPolicy { return s.parts.Load().policy }
func (s *seat) Session() *core.Session     { return s.parts.Load().session }

func (s *seat) turnTo(provider core.Provider, policy core.ContextPolicy) {
	next := *s.parts.Load()
	next.provider, next.policy = provider, policy
	s.parts.Store(&next)
}

func (s *seat) sitDown(session *core.Session, frontend core.Frontend, provider core.Provider, policy core.ContextPolicy) {
	s.parts.Store(&seatParts{provider: provider, frontend: frontend, policy: policy, session: session})
}
