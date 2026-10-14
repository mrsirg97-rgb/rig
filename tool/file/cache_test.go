package file

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/core"
)

func TestRememberedCacheIsBounded(t *testing.T) {
	s := core.NewSession()
	mib := strings.Repeat("x", 1<<20)
	for i := 0; i < 18; i++ {
		rememberContent(s, fmt.Sprintf("/p%02d", i), mib)
	}
	rememberContent(s, "/newest", "newest")

	lastRead.Lock()
	defer lastRead.Unlock()
	if lastRead.bytes > rememberedBytesCap {
		t.Fatalf("the cache holds %d bytes, want at most %d", lastRead.bytes, rememberedBytesCap)
	}
	if _, ok := lastRead.m[rememberedKey(s, "/p00")]; ok {
		t.Fatal("the oldest observation must be evicted when the cap is exceeded")
	}
	if got, ok := lastRead.m[rememberedKey(s, "/newest")]; !ok || got.content != "newest" {
		t.Fatal("the newest observation must survive")
	}
}

func TestRememberedCacheKeysOnTheSession(t *testing.T) {
	sA, sB := core.NewSession(), core.NewSession()
	rememberContent(sA, "/f", "A content")
	rememberContent(sB, "/f", "B content")
	if got, ok := observationOf(sA, "/f"); !ok || got.content != "A content" {
		t.Fatalf("session A's observation = %q/%v, want its own", got.content, ok)
	}
	if got, ok := observationOf(sB, "/f"); !ok || got.content != "B content" {
		t.Fatalf("session B's observation = %q/%v, want its own", got.content, ok)
	}
}

func TestRememberedCacheCarriesTheReadSpan(t *testing.T) {
	s := core.NewSession()
	rememberContent(s, "/w", "window bytes", span{offset: 7, limit: 3})
	rememberContent(s, "/f", "whole bytes")
	window, ok := observationOf(s, "/w")
	if !ok || window.content != "window bytes" || window.at.offset != 7 || window.at.limit != 3 {
		t.Fatalf("a windowed observation must carry its span, got %+v/%v", window, ok)
	}
	whole, ok := observationOf(s, "/f")
	if !ok || whole.at != fullSpan {
		t.Fatalf("a whole observation must carry the full span, got %+v/%v", whole, ok)
	}
}
