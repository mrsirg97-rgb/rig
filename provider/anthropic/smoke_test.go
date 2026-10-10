package anthropic_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/provider/anthropic"
)

func TestLiveSmoke(t *testing.T) {
	model := os.Getenv("RIG_SMOKE_ANTHROPIC_MODEL")
	base := os.Getenv("RIG_SMOKE_ANTHROPIC_BASE_URL")
	key := os.Getenv("RIG_SMOKE_ANTHROPIC_KEY")
	if model == "" || (base == "" && key == "") {
		t.Skip("set RIG_SMOKE_ANTHROPIC_MODEL and either RIG_SMOKE_ANTHROPIC_KEY (a real key, the api default base) or RIG_SMOKE_ANTHROPIC_BASE_URL (a Messages-format server, keyless) to run the live smoke")
	}
	p := anthropic.New(anthropic.Config{
		BaseURL:   base,
		Model:     model,
		APIKey:    key,
		MaxTokens: 256,
	})
	events, err := drain(t, context.Background(), p, core.Request{
		Messages:  []core.Message{{Role: core.RoleUser, Content: "say ok and nothing else"}},
		MaxTokens: 256,
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for _, ev := range events {
		if f, ok := ev.(core.Fault); ok {
			t.Fatalf("the live server faulted: %v", f.Err)
		}
	}
	done := lastDone(t, events)
	if done.StopReason == "" {
		t.Fatal("the live smoke must end with a stop reason")
	}
	if done.Usage.Prompt == 0 || done.Usage.Completion == 0 {
		t.Fatalf("usage = %+v, want the live server's token counts", done.Usage)
	}
	if !strings.HasSuffix(kinds(events), "done") {
		t.Fatalf("events = %s", kinds(events))
	}
}
