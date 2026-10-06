package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
	"github.com/mrsirg97-rgb/rig/v2/testenv"
)

func kernelInterpreter(t *testing.T) string {
	t.Helper()
	if venv, err := exec.LookPath(filepath.Join(testenv.OperatorHome, ".pi", "agent", "kernel-venv", "bin", "python")); err == nil {
		return venv
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no venv interpreter and no python3 on PATH; the kernel cases need one")
	}
	out, err := exec.Command(py, "-c", "import IPython").CombinedOutput()
	if err != nil {
		t.Skipf("IPython not importable by %s (bare box): %s", py, out)
	}
	return py
}

var testRiskQuestion = decision.Question{
	ID: "risk", Kind: decision.KindChoice, Prompt: "What risk does this bash call carry?",
	Choices:     []string{"safe", "changes", "dangerous"},
	Description: map[string]string{"safe": "reads", "changes": "writes", "dangerous": "reaches"},
}

func seedGoldRows(t *testing.T, home string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "decision"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, _, _, err := store.Open(filepath.Join(home, "decision", "decision.sqlite"), decisionstore.Statements(), decisionstore.SchemaVersion, decisionstore.Migration())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB.Close() })
	ctx := core.WithSession(context.Background(), &core.Session{ID: "s1"})
	for i := 0; i < 5; i++ {
		id, err := decisionstore.Propose(ctx, db, decisionstore.ProposeInput{
			Scope: "proj", Site: decision.SiteBash, State: `{"command":"ls"}`,
			Question: testRiskQuestion, Answer: "safe", Confidence: 0.5, Decider: "server",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := decisionstore.Settle(ctx, db, decisionstore.SettleInput{ID: id, Approved: true, Reviewer: "reviewer"}); err != nil {
			t.Fatal(err)
		}
		id, err = decisionstore.Propose(ctx, db, decisionstore.ProposeInput{
			Scope: "proj", Site: decision.SiteBash, State: `{"command":"rm -rf build"}`,
			Question: testRiskQuestion, Answer: "changes", Confidence: 0.5, Decider: "server",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := decisionstore.Settle(ctx, db, decisionstore.SettleInput{ID: id, Approved: false, Reviewer: "reviewer", ReviewerAnswer: "the label is dangerous"}); err != nil {
			t.Fatal(err)
		}
	}
}

const fakeTrainer = `import json, os
def train(rows_path, out_dir):
    ck = os.path.join(out_dir, "ckpt-fake")
    os.makedirs(ck, exist_ok=True)
    return {"checkpoint": ck, "questions": {}}
def evaluate(checkpoint, rows_path):
    n = sum(1 for _ in open(rows_path))
    acc = 0.9 if "ckpt-fake" in checkpoint else 0.5
    return {"questions": {"risk": {"n": n, "correct": round(n * acc), "accuracy": acc}}}
`

const flatTrainer = `import json, os
def train(rows_path, out_dir):
    ck = os.path.join(out_dir, "ckpt-fake")
    os.makedirs(ck, exist_ok=True)
    return {"checkpoint": ck, "questions": {}}
def evaluate(checkpoint, rows_path):
    n = sum(1 for _ in open(rows_path))
    return {"questions": {"risk": {"n": n, "correct": n // 2, "accuracy": 0.5}}}
`

func trainSetup(t *testing.T, trainerSource string, settings func(home string) string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("RIG_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "train"), 0o755); err != nil {
		t.Fatal(err)
	}
	if trainerSource != "" {
		if err := os.WriteFile(filepath.Join(home, "train", "fake.py"), []byte(trainerSource), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if settings != nil {
		if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(settings(home)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seedGoldRows(t, home)
	return home
}

const unitTemplate = `[Unit]
Description=Laya decision server (CPU only, loopback :8095)

[Service]
ExecStart=%h/laya/.venv/bin/python %h/laya/serve_rig.py
Environment=RIG_DECISION_CHECKPOINT=%h/laya/ft-rig-20261006-1658
Environment=LAYA_HOST=127.0.0.1
Environment=LAYA_PORT=8095

[Install]
WantedBy=default.target
`

func runTrain(t *testing.T, trainer string) int {
	t.Helper()
	return decisionTrain([]string{"train", trainer})
}

func runTrainStderr(t *testing.T, trainer string) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	code := decisionTrain([]string{"train", trainer})
	os.Stderr = old
	w.Close()
	out, _ := io.ReadAll(r)
	return code, string(out)
}

func trainingsRow(t *testing.T, home string) (string, bool, *string) {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(home, "decision", "decision.sqlite"), decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	var trainer string
	var promoted bool
	var incumbent *string
	if err := db.QueryRow(`SELECT "trainer", "promoted", "incumbent" FROM "trainings"`).Scan(&trainer, &promoted, &incumbent); err != nil {
		t.Fatalf("the run is recorded: %v", err)
	}
	return trainer, promoted, incumbent
}

func TestAFakeTrainerPromotesOnAMeasuredWin(t *testing.T) {
	home := trainSetup(t, fakeTrainer, func(home string) string {
		return fmt.Sprintf(`{"trainPython": %q, "decisionUnit": %q}`, kernelInterpreter(t), filepath.Join(home, "laya.service"))
	})
	unit := filepath.Join(home, "laya.service")
	if err := os.WriteFile(unit, []byte(unitTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RIG_HOME", home)
	if code := runTrain(t, "fake"); code != 0 {
		t.Fatalf("decision train exit = %d, want 0", code)
	}
	runs, err := os.ReadDir(filepath.Join(home, "decision", "train"))
	if err != nil || len(runs) != 1 {
		t.Fatalf("one run directory: %v, %v", runs, err)
	}
	runDir := filepath.Join(home, "decision", "train", runs[0].Name())
	for _, name := range []string{"rig-train.jsonl", "rig-heldout.jsonl"} {
		b, err := os.ReadFile(filepath.Join(runDir, name))
		if err != nil || !strings.Contains(string(b), `"questions":{"risk"`) {
			t.Fatalf("%s is not in laya's shape: %v", name, err)
		}
	}
	unitText, err := os.ReadFile(unit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unitText), "Environment=RIG_DECISION_CHECKPOINT="+filepath.Join(runDir, "ckpt-fake")) {
		t.Fatalf("the promotion rewrites the checkpoint line:\n%s", unitText)
	}
	if strings.Count(string(unitText), "Environment=") != 3 {
		t.Fatalf("the rewrite touches one line:\n%s", unitText)
	}
	if strings.Contains(string(unitText), "ft-rig-20261006-1658") {
		t.Fatal("the old checkpoint is gone from the unit")
	}
	if _, promoted, _ := trainingsRow(t, home); !promoted {
		t.Fatal("the measured win is recorded promoted")
	}
	if code := runTrain(t, "absent"); code != 1 {
		t.Fatalf("an unknown trainer exits 1, got %d", code)
	}
}

func TestAFakeTrainerThatDoesNotBeatPromotesNothing(t *testing.T) {
	home := trainSetup(t, flatTrainer, func(home string) string {
		return fmt.Sprintf(`{"trainPython": %q, "decisionUnit": %q}`, kernelInterpreter(t), filepath.Join(home, "laya.service"))
	})
	unit := filepath.Join(home, "laya.service")
	if err := os.WriteFile(unit, []byte(unitTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RIG_HOME", home)
	if code := runTrain(t, "fake"); code != 0 {
		t.Fatalf("decision train exit = %d, want 0", code)
	}
	unitText, _ := os.ReadFile(unit)
	if !strings.Contains(string(unitText), "ft-rig-20261006-1658") {
		t.Fatalf("a tie promotes nothing, the unit moved:\n%s", unitText)
	}
	if _, promoted, _ := trainingsRow(t, home); promoted {
		t.Fatal("the tie is recorded promoted=false")
	}
}

func TestTrainingWithNoUnitScoresAndPromotesNothing(t *testing.T) {
	home := trainSetup(t, flatTrainer, func(home string) string {
		return fmt.Sprintf(`{"trainPython": %q}`, kernelInterpreter(t))
	})
	t.Setenv("RIG_HOME", home)
	if code := runTrain(t, "fake"); code != 0 {
		t.Fatalf("decision train exit = %d, want 0", code)
	}
	_, promoted, incumbent := trainingsRow(t, home)
	if promoted || incumbent != nil {
		t.Fatalf("no unit means no incumbent and no promotion: %v %v", promoted, incumbent)
	}
}

func TestTrainingRefusesUsage(t *testing.T) {
	trainSetup(t, "", nil)
	if code := decisionTrain([]string{"train"}); code != 2 {
		t.Fatalf("usage exits 2, got %d", code)
	}
	if code := decisionTrain(nil); code != 2 {
		t.Fatalf("no args exits 2, got %d", code)
	}
}

func TestTrainingWithoutTrainPythonRefusesNamingTheKey(t *testing.T) {
	home := trainSetup(t, fakeTrainer, func(home string) string {
		return `{"decisionUnit": "/x/laya.service"}`
	})
	t.Setenv("RIG_HOME", home)
	code, errText := runTrainStderr(t, "fake")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errText, "trainPython") {
		t.Fatalf("the refusal names the key: %q", errText)
	}
	if _, err := os.Stat(filepath.Join(home, "decision", "train")); err == nil {
		t.Fatal("no interpreter, no run directory")
	}
}

func TestTheUnitRewriteTouchesOneLineOnly(t *testing.T) {
	unit := filepath.Join(t.TempDir(), "laya.service")
	if err := os.WriteFile(unit, []byte(unitTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	line, err := rewriteUnit(unit, "/h/laya/ft-new")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "daemon-reload") || !strings.Contains(line, "restart laya") {
		t.Fatalf("the reply names the operator's restart: %q", line)
	}
	text, _ := os.ReadFile(unit)
	want := strings.Replace(unitTemplate, "Environment=RIG_DECISION_CHECKPOINT=%h/laya/ft-rig-20261006-1658", "Environment=RIG_DECISION_CHECKPOINT=/h/laya/ft-new", 1)
	if string(text) != want {
		t.Fatalf("only the checkpoint line moved:\n%s", text)
	}
	if ckpt, err := unitCheckpoint(unit); err != nil || ckpt != "/h/laya/ft-new" {
		t.Fatalf("the incumbent read: %q, %v", ckpt, err)
	}
	if _, err := rewriteUnit(filepath.Join(t.TempDir(), "absent.service"), "/x"); err == nil {
		t.Fatal("no unit file refuses")
	}
	bare := filepath.Join(t.TempDir(), "bare.service")
	if err := os.WriteFile(bare, []byte("[Service]\nNice=10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := rewriteUnit(bare, "/x"); err == nil || !strings.Contains(err.Error(), "RIG_DECISION_CHECKPOINT") {
		t.Fatalf("a unit without the checkpoint line refuses: %v", err)
	}
}

func TestTheIncumbentExpandsTheHomeTilde(t *testing.T) {
	unit := filepath.Join(t.TempDir(), "laya.service")
	if err := os.WriteFile(unit, []byte(unitTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	ckpt, err := unitCheckpoint(unit)
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if ckpt != filepath.Join(home, "laya/ft-rig-20261006-1658") {
		t.Fatalf("%%h expands to the operator's home: %q", ckpt)
	}
}
