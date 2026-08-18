package command_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/models"
)

// table is the 0.2.0 rows with their roles (SPEC_CONFIG 4: the table
// leaves code for the embedded config/models.json; the test harnesses
// construct the same rows).
func table() models.Table {
	t, err := models.New(
		models.Model{ID: "local", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleInteractive},
		models.Model{ID: "qwen3.8-workers", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleWorker},
	)
	if err != nil {
		panic("command: table: " + err.Error())
	}
	return t
}

// TestModelsListMarksActive (SPEC_COMMANDS, named): the exact lines —
// window, max, reserve, keep, trigger (Window - Reserve), the active
// row marked, the raw token counts (greppable) — now with the role
// column (SPEC_CONFIG 4).
func TestModelsListMarksActive(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Models:      func() models.Table { return table() },
		ActiveModel: func() string { return "local" },
	}
	out, err := byName["models"].Run(context.Background(), "", env)
	if err != nil {
		t.Fatal(err)
	}
	want := "local            interactive  window 65536  max 8192  reserve 8192  keep 16384  trigger 57344  *\n" +
		"qwen3.8-workers  worker       window 65536  max 8192  reserve 8192  keep 16384  trigger 57344\n"
	if out != want {
		t.Fatalf("the table lines must be exact:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

// TestModelsListShowsRoleColumn (SPEC_CONFIG 4, named): the /models
// line gains the role column after the id; the listing order is stable
// (sorted by id, the table's Known() order).
func TestModelsListShowsRoleColumn(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		Models:      func() models.Table { return table() },
		ActiveModel: func() string { return "qwen3.8-workers" },
	}
	out, err := byName["models"].Run(context.Background(), "", env)
	if err != nil {
		t.Fatal(err)
	}
	// the exact lines, id-sorted, the role between the id and the
	// numbers, the active marker on the worker row.
	want := "local            interactive  window 65536  max 8192  reserve 8192  keep 16384  trigger 57344\n" +
		"qwen3.8-workers  worker       window 65536  max 8192  reserve 8192  keep 16384  trigger 57344  *\n"
	if out != want {
		t.Fatalf("the table lines must be exact:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

func TestModelsRefusalPassesThrough(t *testing.T) {
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

func TestModelsSwitchCallsTheSeam(t *testing.T) {
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
