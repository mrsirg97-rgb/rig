package todo_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	todoapi "github.com/mrsirg97-rgb/rig/tool/todo"
)

func taskIDText(t *testing.T, reply, text string) string {
	t.Helper()
	re := regexp.MustCompile(`\bt(\d+)\b \[[~x! ]\] ` + regexp.QuoteMeta(text))
	if mm := re.FindStringSubmatch(reply); mm != nil {
		return "t" + mm[1]
	}
	t.Fatalf("no task %q in:\n%s", text, reply)
	return ""
}

func TestReleaseIsAStateVerbAndNeedsAnID(t *testing.T) {
	tool := todoapi.New(newDB(t))
	if _, err := exec(t, tool, context.Background(), map[string]any{"action": "release"}); err == nil {
		t.Fatal("release without id succeeded")
	} else if want := "action 'release' requires id"; err.Error() != want {
		t.Errorf("voice:\n%q\nwant\n%q", err.Error(), want)
	}
}

func TestReleaseRefusesAFreshForeignClaimThroughTheTool(t *testing.T) {
	tool := todoapi.New(newDB(t))
	ctx := context.Background()
	created, err := exec(t, tool, ctx, map[string]any{"action": "create", "tasks": []any{map[string]any{"text": "wire the guard"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := taskIDText(t, created, "wire the guard")
	if _, err := exec(t, tool, ctx, map[string]any{"action": "start", "id": id}); err != nil {
		t.Fatalf("start: %v", err)
	}
	// A second session (different context session id) cannot release a
	// live claim: the door stays closed until the claim is stale.
	s := core.NewSession()
	s.ID = "another-session"
	sctx := core.WithSession(ctx, s)
	_, err = exec(t, tool, sctx, map[string]any{"action": "release", "id": id})
	if err == nil {
		t.Fatal("a live foreign claim was released")
	} else if !strings.Contains(err.Error(), "claimed by") {
		t.Errorf("fresh-claim refusal must name the claim: %v", err)
	}
}
