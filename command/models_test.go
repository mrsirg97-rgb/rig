package command_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/models"
)

// TestModelsListMarksActive (SPEC_COMMANDS, named): the exact lines —
// window, max, reserve, keep, trigger (Window - Reserve), the active
// row marked, the raw token counts (greppable).
func TestModelsListMarksActive(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Models:      func() models.Table { return models.Defaults },
		ActiveModel: func() string { return "local" },
	}
	out, err := byName["models"].Run(context.Background(), "", env)
	if err != nil {
		t.Fatal(err)
	}
	want := "local            window 65536  max 8192  reserve 8192  keep 16384  trigger 57344  *\n" +
		"qwen3.8-workers  window 65536  max 8192  reserve 8192  keep 16384  trigger 57344\n"
	if out != want {
		t.Fatalf("the table lines must be exact:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

// TestModelsSwitchUnknownNamesKnown (SPEC_COMMANDS, named): the
// unknown id is loud, naming the known — the seam's voice passes
// through verbatim.
func TestModelsSwitchUnknownNamesKnown(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		SwitchModel: func(ctx context.Context, id string) error {
			return errors.New(`models: no row for "nope" (known: local, qwen3.8-workers)`)
		},
	}
	out, err := byName["models"].Run(context.Background(), "nope", env)
	if err == nil || out != "" || err.Error() != `models: no row for "nope" (known: local, qwen3.8-workers)` {
		t.Fatalf("an unknown id must be loud naming the known, got (%q, %v)", out, err)
	}
}

// TestModelsSwitchLine (SPEC_COMMANDS 6): the switch output names the
// new active id.
func TestModelsSwitchLine(t *testing.T) {
	byName := allByName(t)
	switched := ""
	env := &command.Env{
		SwitchModel: func(ctx context.Context, id string) error {
			switched = id
			return nil
		},
	}
	out, err := byName["models"].Run(context.Background(), "qwen3.8-workers", env)
	if err != nil || out != "models: active is now qwen3.8-workers" || switched != "qwen3.8-workers" {
		t.Fatalf("the switch line = (%q, %v) with id %q", out, err, switched)
	}
}

// TestModelsUsage (SPEC_COMMANDS 6): two or more args refuse, naming
// the shape.
func TestModelsUsage(t *testing.T) {
	byName := allByName(t)
	_, err := byName["models"].Run(context.Background(), "a b", &command.Env{})
	if err == nil || err.Error() != "models: usage: models [<id>]" {
		t.Fatalf("two args must refuse with the usage line, got %v", err)
	}
}
