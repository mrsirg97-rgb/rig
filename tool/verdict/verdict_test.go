package verdict_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/tool/verdict"
)

type wire struct {
	mu   sync.Mutex
	sent []broadcast.Message
}

func (w *wire) Id() int64 { return 9 }

func (w *wire) Send(ctx context.Context, callback func(error), messages ...broadcast.Message) {
	w.mu.Lock()
	w.sent = append(w.sent, messages...)
	w.mu.Unlock()
	callback(nil)
}

func (w *wire) Recv(context.Context, func(error, ...broadcast.Message)) {}

func (w *wire) Close() {}

func TestAnAcceptAndARejectCrossTheFleetAsTheWorker(t *testing.T) {
	fleet := &wire{}
	tool := verdict.New(fleet)
	if tool.Name() != "verdict" {
		t.Fatalf("name = %q", tool.Name())
	}
	reply, err := tool.Exec(context.Background(), json.RawMessage(`{"accept":true}`))
	if err != nil || reply != "recorded" {
		t.Fatalf("accept = %q %v", reply, err)
	}
	if _, err := tool.Exec(context.Background(), json.RawMessage(`{"row":4,"accept":false,"reason":" the tests are missing "}`)); err != nil {
		t.Fatal(err)
	}
	if len(fleet.sent) != 2 || fleet.sent[0].Origin() != 9 {
		t.Fatalf("two verdicts cross as member 9, got %+v", fleet.sent)
	}
	v := fleet.sent[1].Event().(core.Verdict)
	if v.Row != 4 || v.Accept || v.Reason != "the tests are missing" {
		t.Fatalf("the reject carries its row and trimmed reason: %+v", v)
	}
}

func TestARejectWithoutAReasonRefusesBeforeTheWire(t *testing.T) {
	fleet := &wire{}
	tool := verdict.New(fleet)
	_, err := tool.Exec(context.Background(), json.RawMessage(`{"accept":false,"reason":"  "}`))
	if err == nil || !strings.Contains(err.Error(), "needs the reason") {
		t.Fatalf("err = %v", err)
	}
	if _, err := tool.Exec(context.Background(), json.RawMessage(`{"accept":true,"verdict":"yes"}`)); err == nil {
		t.Fatal("an unknown field refuses")
	}
	if len(fleet.sent) != 0 {
		t.Fatalf("nothing crossed: %+v", fleet.sent)
	}
}

func TestTheToolNeedsAFleet(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a verdict tool without a fleet is a construction error")
		}
	}()
	verdict.New(nil)
}
