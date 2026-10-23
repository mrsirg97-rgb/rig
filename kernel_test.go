package rig

import (
	"context"
	"encoding/json"
	"github.com/mrsirg97-rgb/evt"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/core"
)

type stubCmd struct{ name string }

func (s stubCmd) Name() string        { return s.name }
func (s stubCmd) Description() string { return "a test command" }
func (s stubCmd) Run(ctx context.Context, args string, env any) (string, error) {
	return s.name + ": " + args, nil
}

func TestWithCommandsRegistersTheRegistry(t *testing.T) {
	k := New(WithCommands(stubCmd{name: "alpha"}, stubCmd{name: "beta"}))
	if len(k.Commands()) != 2 {
		t.Fatalf("kernel must carry the registered commands, got %d", len(k.Commands()))
	}
	if k.Commands()[0].Name() != "alpha" || k.Commands()[1].Name() != "beta" {
		t.Fatalf("registration order must be kept: %v", namesOf(k.Commands()))
	}
}

func TestWithCommandsDuplicateNamePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("a duplicate command name must panic at startup, loud and early (the tools' precedent)")
		} else if !strings.Contains(r.(string), "duplicate command name") {
			t.Fatalf("the panic must name the duplication: %v", r)
		}
	}()
	New(WithCommands(stubCmd{name: "dup"}, stubCmd{name: "other"}, stubCmd{name: "dup"}))
}

func TestKernelWithoutCommandsIsUnchanged(t *testing.T) {
	k := New()
	if k.Commands() != nil {
		t.Fatalf("the registry must be empty (nil) when unregistered, got %v", k.Commands())
	}
}

func namesOf(cmds []core.Command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c.Name())
	}
	return out
}

var _ core.Command = stubCmd{}

func TestCommandSeamShape(t *testing.T) {
	var c core.Command = stubCmd{name: "x"}
	out, err := c.Run(context.Background(), "args", struct{}{})
	if err != nil || out != "x: args" {
		t.Fatalf("the seam must carry (ctx, args, env any) and return (string, error): %q %v", out, err)
	}
	if _, err := c.Run(context.Background(), "args", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("the env stays untyped at the seam: %v", err)
	}
}

func TestWithEngineHandsTheLoopItsEngine(t *testing.T) {
	e := evt.NewEngine()
	k := New(WithEngine(e))
	if k.Engine() != e {
		t.Fatal("the kernel must carry the engine it was given")
	}
	if New().Engine() != nil {
		t.Fatal("a kernel built without one has none; the loop mints its own")
	}
	if !(PriorityInput > PriorityDelegate && PriorityDelegate > PriorityStream && PriorityStream >= PriorityTool && PriorityTool > PriorityFleet && PriorityFleet > PriorityReview) {
		t.Fatalf("the review runs below the fleet, the fleet below the delegate, the delegate below the turn, the turn below the operator: %d %d %d %d %d %d", PriorityInput, PriorityDelegate, PriorityStream, PriorityTool, PriorityFleet, PriorityReview)
	}
}

type swapSeat struct{ session *core.Session }

func (s *swapSeat) Provider() core.Provider    { return nil }
func (s *swapSeat) Frontend() core.Frontend    { return nil }
func (s *swapSeat) Policy() core.ContextPolicy { return nil }
func (s *swapSeat) Session() *core.Session     { return s.session }

func TestAKernelReadsItsSeatAtEveryUse(t *testing.T) {
	seat := &swapSeat{session: core.NewSession()}
	k := New(WithSeat(seat))
	next := core.NewSession()
	seat.session = next
	if k.Session() != next {
		t.Fatal("the kernel must read the seat at the call, so a swap at the root lands without writing the kernel")
	}
}

func TestAFixedSeatHasASession(t *testing.T) {
	if New().Session() == nil {
		t.Fatal("a kernel built from fixed parts mints its session when none is named")
	}
	s := core.NewSession()
	if New(WithSession(s)).Session() != s {
		t.Fatal("a named session is the kernel's session")
	}
}

func TestASeatBesideFixedPartsPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "a seat or its fixed parts") {
			t.Fatalf("a kernel given a seat and a fixed part must refuse at construction, naming both: %v", r)
		}
	}()
	New(WithSeat(&swapSeat{}), WithSession(core.NewSession()))
}
