package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/tool"
)

func TestEveryWiredToolHasARegistryEntryAndEveryEntryIsWired(t *testing.T) {
	entries := map[string]bool{}
	for _, name := range tool.AllNames() {
		entries[name] = true
	}
	wired := map[string]bool{}
	for _, name := range nativeToolNames {
		wired[name] = true
		if !entries[name] {
			t.Errorf("native %q is wired but has no entry in tool/registry.json", name)
		}
	}
	for name := range conditionalNatives {
		wired[name] = true
		if !entries[name] {
			t.Errorf("conditional native %q has no entry in tool/registry.json", name)
		}
	}
	for name := range entries {
		if !wired[name] {
			t.Errorf("tool/registry.json names %q but the root wires no such tool", name)
		}
	}
}

func TestNativeNamesFollowTheRegistryOrder(t *testing.T) {
	want := []string{}
	for _, name := range tool.Names() {
		if !conditionalNatives[name] {
			want = append(want, name)
		}
	}
	if strings.Join(want, ",") != strings.Join(nativeToolNames, ",") {
		t.Fatalf("nativeToolNames = %v, want the registry's enabled order %v", nativeToolNames, want)
	}
}

func TestNoToolWordsLiveOutsideTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "node_modules", "site":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), "Guidelines: ") || strings.Contains(string(b), " Reply: ") {
			rel, _ := filepath.Rel(root, path)
			if rel != filepath.Join("tool", "registry.go") {
				offenders = append(offenders, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("tool words outside tool/registry.json, in: %v", offenders)
	}
}
