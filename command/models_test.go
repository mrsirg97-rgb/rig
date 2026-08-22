package command_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mrsirg97-rgb/rig/command"
	"github.com/mrsirg97-rgb/rig/models"
)

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

	want := "local            interactive  window 65536  max 8192  reserve 8192  keep 16384  trigger 57344\n" +
		"qwen3.8-workers  worker       window 65536  max 8192  reserve 8192  keep 16384  trigger 57344  *\n"
	if out != want {
		t.Fatalf("the table lines must be exact:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

func TestModelsRefusalPassesThrough(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		SwitchModel: func(ctx context.Context, id string) (string, error) {
			return "", errors.New(`models: no row for "nope" (known: local, qwen3.8-workers)`)
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
		SwitchModel: func(ctx context.Context, id string) (string, error) {
			switched = id
			return "", nil
		},
	}
	out, err := byName["models"].Run(context.Background(), "qwen3.8-workers", env)
	if err != nil || out != "models: active is now qwen3.8-workers" || switched != "qwen3.8-workers" {
		t.Fatalf("the switch line = (%q, %v) with id %q", out, err, switched)
	}
}

func TestModelsSwitchCarriesTheNote(t *testing.T) {
	byName := allByName(t)
	env := &command.Env{
		SwitchModel: func(ctx context.Context, id string) (string, error) {
			return `effort: "xhigh" is not a level for qwen3.8-workers — reset to server default`, nil
		},
	}
	out, err := byName["models"].Run(context.Background(), "qwen3.8-workers", env)
	want := "models: active is now qwen3.8-workers\n" +
		`effort: "xhigh" is not a level for qwen3.8-workers — reset to server default`
	if err != nil || out != want {
		t.Fatalf("the note must ride the reply:\ngot  (%q, %v)\nwant %q", out, err, want)
	}
}
