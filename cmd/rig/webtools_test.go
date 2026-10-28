package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/webtools"
)

func TestTheConfigDoorCarriesTheKeyIntoSettingsJSON(t *testing.T) {
	home := t.TempDir()
	var door webtools.Setting = &configSetting{dir: home}
	if err := door.Put("webFetchProxy", "http://rig:hunter2longenough@127.0.0.1:8889"); err != nil {
		t.Fatal(err)
	}
	v, ok, err := door.Read("webFetchProxy")
	if err != nil || !ok || !strings.Contains(v, "127.0.0.1:8889") {
		t.Fatalf("Read = %q %v %v", v, ok, err)
	}
	if _, ok, err := door.Read("allow"); err != nil || ok {
		t.Fatalf("an unset key = %v %v, want not-set with no error", ok, err)
	}
	if _, _, err := door.Read("webFetchProxxy"); err == nil {
		t.Fatal("a key config does not know must be refused")
	}
	b, err := os.ReadFile(filepath.Join(home, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "webFetchProxy") {
		t.Fatalf("settings.json = %s", b)
	}
}

func TestTheWiredToolsAnswerFromTheirOwnHome(t *testing.T) {
	home := t.TempDir()
	tools := newWebTools(home)
	if got := tools.Dir(); got != filepath.Join(home, "external") {
		t.Fatalf("dir = %q, want the home's external directory", got)
	}
	st, err := tools.Status(context.Background())
	if err != nil {
		t.Fatalf("Status must answer whether or not docker is here: %v", err)
	}
	if st.Docker == "" {
		t.Fatalf("docker = %q, want the daemon's voice or its absence reported", st.Docker)
	}
	if !strings.Contains(strings.Join(st.Lines(), "\n"), "docker") {
		t.Fatalf("lines = %v", st.Lines())
	}
}
