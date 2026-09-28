package file

import (
	"sync"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

var rememberedBytesCap = 16 << 20

var lastRead = struct {
	sync.Mutex
	m     map[string]string
	order []string
	bytes int
}{m: map[string]string{}}

func rememberedKey(s *core.Session, path string) string { return s.ID + "\x00" + path }

func rememberContent(s *core.Session, path, content string) {
	if s == nil {
		return
	}
	key := rememberedKey(s, path)
	lastRead.Lock()
	defer lastRead.Unlock()
	if prev, exists := lastRead.m[key]; exists {
		lastRead.bytes += len(content) - len(prev)
	} else {
		lastRead.order = append(lastRead.order, key)
		lastRead.bytes += len(content)
	}
	lastRead.m[key] = content
	for lastRead.bytes > rememberedBytesCap {
		oldest := lastRead.order[0]
		lastRead.order = lastRead.order[1:]
		lastRead.bytes -= len(lastRead.m[oldest])
		delete(lastRead.m, oldest)
	}
}

func forgottenContent(s *core.Session, path string) (string, bool) {
	if s == nil {
		return "", false
	}
	lastRead.Lock()
	defer lastRead.Unlock()
	c, ok := lastRead.m[rememberedKey(s, path)]
	return c, ok
}
