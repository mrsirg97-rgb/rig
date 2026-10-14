package file

import (
	"sync"

	"github.com/mrsirg97-rgb/rig/v3/core"
)

const rememberedBytesCap = 16 << 20

type span struct {
	offset, limit int
}

var fullSpan = span{offset: 0, limit: -1}

type observation struct {
	content string
	at      span
}

var lastRead = struct {
	sync.Mutex
	m     map[string]observation
	order []string
	bytes int
}{m: map[string]observation{}}

func rememberedKey(s *core.Session, path string) string { return s.ID + "\x00" + path }

func rememberContent(s *core.Session, path, content string, at ...span) {
	if s == nil {
		return
	}
	obs := observation{content: content, at: fullSpan}
	if len(at) > 0 {
		obs.at = at[0]
	}
	key := rememberedKey(s, path)
	lastRead.Lock()
	defer lastRead.Unlock()
	if prev, exists := lastRead.m[key]; exists {
		lastRead.bytes += len(content) - len(prev.content)
	} else {
		lastRead.order = append(lastRead.order, key)
		lastRead.bytes += len(content)
	}
	lastRead.m[key] = obs
	for lastRead.bytes > rememberedBytesCap {
		oldest := lastRead.order[0]
		lastRead.order = lastRead.order[1:]
		lastRead.bytes -= len(lastRead.m[oldest].content)
		delete(lastRead.m, oldest)
	}
}

func observationOf(s *core.Session, path string) (observation, bool) {
	if s == nil {
		return observation{}, false
	}
	lastRead.Lock()
	defer lastRead.Unlock()
	o, ok := lastRead.m[rememberedKey(s, path)]
	return o, ok
}
