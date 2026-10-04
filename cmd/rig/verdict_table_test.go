package main

import (
	"context"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/tool/verdict"
)

type quietWire struct{}

func (quietWire) Id() int64                                               { return 9 }
func (quietWire) Send(context.Context, func(error), ...broadcast.Message) {}
func (quietWire) Recv(context.Context, func(error, ...broadcast.Message)) {}
func (quietWire) Close()                                                  {}

func TestAWorkerWithAFleetIsOfferedTheVerdictTool(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.tools["verdict"] = verdict.New(quietWire{})
	k := wire(r)
	offered := false
	for _, tl := range k.Tools {
		if tl.Name() == "verdict" {
			offered = true
		}
	}
	if !offered {
		t.Fatal("the verdict tool is registered but not on the table the model reads")
	}
	plain := wire(testRoot(nullFrontend{}))
	for _, tl := range plain.Tools {
		if tl.Name() == "verdict" {
			t.Fatal("a session without a fleet must not offer verdict")
		}
	}
}
