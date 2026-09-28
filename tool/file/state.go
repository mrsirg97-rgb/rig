package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

var filesMu sync.Mutex

func stateOf(ctx context.Context, path string) (core.FileState, bool) {
	s, ok := core.SessionFrom(ctx)
	if !ok {
		return core.FileState{}, false
	}
	filesMu.Lock()
	defer filesMu.Unlock()
	st, ok := s.Files[path]
	return st, ok
}

func SnapshotFiles(s *core.Session) map[string]core.FileState {
	if s == nil {
		return nil
	}
	filesMu.Lock()
	defer filesMu.Unlock()
	out := make(map[string]core.FileState, len(s.Files))
	for p, st := range s.Files {
		out[p] = st
	}
	return out
}

func recordState(ctx context.Context, path string, data []byte) {
	sum := sha256.Sum256(data)
	recordDigest(ctx, path, sum)
}

func recordDigest(ctx context.Context, path string, sum [32]byte) {
	s, ok := core.SessionFrom(ctx)
	if !ok {
		return
	}
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	filesMu.Lock()
	defer filesMu.Unlock()
	s.Files[path] = core.FileState{
		Hash:  hex.EncodeToString(sum[:]),
		Mtime: st.ModTime().UnixNano(),
	}
}
