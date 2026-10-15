package testenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAWallCrontabRefusesAnUnbuildableBin(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.WriteFile(home, []byte("a file, not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := wallCrontab(home)
	if err == nil {
		t.Fatal("a home whose bin cannot be created must fail the crontab shim, not pass silently")
	}
	if !strings.Contains(err.Error(), "crontab shim") {
		t.Fatalf("the refusal must name the step, got %q", err)
	}
}

func TestAMkTestHomeRefusesAnUnusableTmp(t *testing.T) {
	base := t.TempDir()
	ro := filepath.Join(base, "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", filepath.Join(ro, "tmp"))
	home, err := mkTestHome("")
	if err == nil {
		if home != "" {
			os.RemoveAll(home)
		}
		t.Fatal("an unusable TMPDIR must fail the test home, not pass silently")
	}
	if home != "" {
		t.Fatalf("a failed home must come back empty, got %q", home)
	}
	if !strings.Contains(err.Error(), "build the test home") {
		t.Fatalf("the refusal must name the step, got %q", err)
	}
}
