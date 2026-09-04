package state_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/store/state"
	"github.com/mrsirg97-rgb/rig/tool/file"
)

type probeFe struct{}

func (probeFe) Input(context.Context) (string, error) { return "hi", nil }
func (probeFe) Notify(core.Event)                     {}

func TestRecorderUpsertDoesNotRaceTheFileTool(t *testing.T) {
	db := openStore(t)
	s := core.NewSession()
	ctx := core.WithSession(context.Background(), s)
	rec := state.NewRecorder(probeFe{}, db, t.TempDir(), "m", "v", s.ID, s)
	if err := rec.Ensure(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "f.txt")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 300; i++ {
			os.WriteFile(p, []byte("x"), 0o644)
			args, _ := json.Marshal(map[string]string{"path": p})
			if _, err := file.Read().Exec(ctx, args); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for i := 0; i < 300; i++ {
		if _, err := rec.Input(ctx); err != nil {
			t.Fatal(err)
		}
	}
	<-done
}
