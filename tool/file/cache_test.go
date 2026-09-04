package file

import (
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/core"
)

func TestRememberedCacheIsBounded(t *testing.T) {
	old := rememberedBytesCap
	rememberedBytesCap = 64
	defer func() { rememberedBytesCap = old }()

	s := core.NewSession()
	rememberContent(s, "/a", strings.Repeat("x", 40))
	rememberContent(s, "/b", strings.Repeat("y", 40))

	lastRead.Lock()
	defer lastRead.Unlock()
	if lastRead.bytes > rememberedBytesCap {
		t.Fatalf("the cache holds %d bytes, want at most %d", lastRead.bytes, rememberedBytesCap)
	}
	if _, ok := lastRead.m[rememberedKey(s, "/a")]; ok {
		t.Fatal("the oldest observation must be evicted when the cap is exceeded")
	}
	if lastRead.m[rememberedKey(s, "/b")] != strings.Repeat("y", 40) {
		t.Fatal("the newest observation must survive")
	}
}

func TestRememberedCacheKeysOnTheSession(t *testing.T) {
	sA, sB := core.NewSession(), core.NewSession()
	rememberContent(sA, "/f", "A content")
	rememberContent(sB, "/f", "B content")
	if got, ok := forgottenContent(sA, "/f"); !ok || got != "A content" {
		t.Fatalf("session A's observation = %q/%v, want its own", got, ok)
	}
	if got, ok := forgottenContent(sB, "/f"); !ok || got != "B content" {
		t.Fatalf("session B's observation = %q/%v, want its own", got, ok)
	}
}
