package decision

import (
	"context"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/models"
)

type haltedReviews struct{}

func (haltedReviews) Settled(context.Context, string, Question, string) (string, bool, error) {
	return "", false, nil
}

func (haltedReviews) Pending(context.Context) ([]ReviewRow, error) {
	return []ReviewRow{{ID: 1, Scope: "proj", Site: "s", State: "st", Question: Question{Prompt: "q"}}}, nil
}

func (haltedReviews) Settle(context.Context, int64, bool, string, string) error { return nil }

func TestAHaltedFireClearsTheVerdicts(t *testing.T) {
	eng := evt.NewEngine()
	run, stopEngine := context.WithCancel(context.Background())
	defer stopEngine()
	go eng.Start(run)
	room := broadcast.NewRoom("halt", func(origin int64) broadcast.Transport {
		return broadcast.NewLoopTransport(origin, eng, 30)
	})
	fire := func(ctx context.Context, prompt string, voice broadcast.Member) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	r := NewReviewer(context.Background(), eng, haltedReviews{}, fire, 4,
		models.Model{Window: 1 << 30, Reserve: 0, MaxTokens: 1 << 30}, room, "proj")
	r.verdicts[1] = core.Verdict{Row: 1, Reason: "carry me"}
	r.Land()
	r.Wake()
	deadline := time.Now().Add(5 * time.Second)
	for r.speaking.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the fire never began")
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.Halt()
	deadline = time.Now().Add(2 * time.Second)
	for r.verdictCount() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("a halted fire must clear the verdicts it gathered: %v", r.verdictCount())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
