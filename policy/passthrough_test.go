package policy_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/mrsirg97-rgb/looper/core"
	"github.com/mrsirg97-rgb/looper/policy"
)

func TestAssemblesSystemPlusTranscriptVerbatim(t *testing.T) {
	session := core.NewSession()
	session.Append(core.Message{Role: core.RoleUser, Content: "first"})
	session.Append(core.Message{Role: core.RoleAssistant, Content: "second"})

	p := policy.Passthrough("be terse")
	got, err := p.Assemble(context.Background(), session)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	want := []core.Message{
		{Role: core.RoleSystem, Content: "be terse"},
		{Role: core.RoleUser, Content: "first"},
		{Role: core.RoleAssistant, Content: "second"},
	}
	if len(got) != len(want) {
		t.Fatalf("assembled %d messages, want %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("position %d diverged:\n got: %+v\nwant: %+v", i, got[i], want[i])
		}
	}
}

func TestEmptyTranscriptSystemOnly(t *testing.T) {
	p := policy.Passthrough("be terse")
	got, err := p.Assemble(context.Background(), core.NewSession())
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(got) != 1 || got[0].Role != core.RoleSystem {
		t.Fatalf("empty transcript must assemble to the system prompt alone, got %+v", got)
	}
}

func TestNoSystemNoTranscript(t *testing.T) {
	p := policy.Passthrough("")
	got, err := p.Assemble(context.Background(), core.NewSession())
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("zero-length boundary must assemble empty, got %+v", got)
	}
}

func TestAssemblyIsPure(t *testing.T) {
	session := core.NewSession()
	session.Append(core.Message{Role: core.RoleUser, Content: "hi"})
	p := policy.Passthrough("sys")

	first, err := p.Assemble(context.Background(), session)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	second, err := p.Assemble(context.Background(), session)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("passthrough must be pure across repeated assemblies")
	}
}
