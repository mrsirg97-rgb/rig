package file_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/tool/file"
)

func TestReadsRecordFileStateConcurrently(t *testing.T) {
	dir := t.TempDir()
	const n = 16
	paths := make([]string, n)
	for i := range paths {
		paths[i] = filepath.Join(dir, "f"+strconv.Itoa(i)+".txt")
		if err := os.WriteFile(paths[i], []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := core.NewSession()
	ctx := core.WithSession(context.Background(), s)
	read := file.Read()
	var wg sync.WaitGroup
	for _, p := range paths {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			args, _ := json.Marshal(map[string]string{"path": p})
			if _, err := read.Exec(ctx, args); err != nil {
				t.Errorf("read %s: %v", p, err)
			}
		}(p)
	}
	wg.Wait()
	if len(s.Files) != n {
		t.Fatalf("recorded %d file states, want %d", len(s.Files), n)
	}
}
