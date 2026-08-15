package state_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// SPEC_STATE's generation-drift guard: regenerate into a temp root against
// the committed metadata and diff against the committed generated files.
// Drift fails the build with the regenerate command in the message.
func TestGeneratedMatchesCommitted(t *testing.T) {
	liftCmd, err := filepath.Abs(filepath.Join(os.Getenv("HOME"), "Projects", "lift", "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(liftCmd, "main.go")); err != nil {
		t.Skip("lift checkout absent")
	}
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := os.MkdirTemp("", "stategen")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	regenerate := func() {
		genCfg := map[string]any{}
		if err := json.Unmarshal(mustReadJSON(t, filepath.Join(workDir, "gen.json")), &genCfg); err != nil {
			t.Fatalf("gen.json: %v", err)
		}
		genCfg["name"] = tmp
		tmpGen, err := writeJSON(t, filepath.Join(tmp, "gen.json"), genCfg)
		if err != nil {
			t.Fatal(err)
		}
		srcCfg := map[string]any{}
		if err := json.Unmarshal(mustReadJSON(t, filepath.Join(workDir, "source.json")), &srcCfg); err != nil {
			t.Fatalf("source.json: %v", err)
		}
		srcCfg["sourceDirectory"] = workDir // the committed metadata lives beside this package
		tmpSrc, err := writeJSON(t, filepath.Join(tmp, "source.json"), srcCfg)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("go", "run", "main.go", "-config="+tmpGen, "-source="+tmpSrc)
		cmd.Dir = liftCmd
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("regeneration: %v\n%s", err, out)
		}
	}
	regenerate()

	regenCmd := "cd <lift>/cmd && go run main.go -config=$LOOPER/store/state/gen.json -source=$LOOPER/store/state/source.json"
	for _, pkg := range []string{"domain", "ddl"} {
		committed := listFiles(t, filepath.Join(workDir, pkg))
		got := listFiles(t, filepath.Join(tmp, pkg))
		var missing []string
		for name, cb := range committed {
			b, ok := got[name]
			if !ok {
				missing = append(missing, pkg+"/"+name)
				continue
			}
			if !bytes.Equal(cb, b) {
				t.Fatalf("drift in %s/%s (regenerate: %s)", pkg, name, regenCmd)
			}
		}
		if len(missing) != 0 {
			sort.Strings(missing)
			t.Fatalf("missing generated files %v (regenerate: %s)", missing, regenCmd)
		}
	}
}

func mustReadJSON(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func writeJSON(t *testing.T, path string, v any) (string, error) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func listFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out[rel] = b
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}
