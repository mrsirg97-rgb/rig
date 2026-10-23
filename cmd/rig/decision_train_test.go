package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v3/core"
	"github.com/mrsirg97-rgb/rig/v3/decision"
	"github.com/mrsirg97-rgb/rig/v3/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v3/store/decision"
	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v3/testenv"
	pythontool "github.com/mrsirg97-rgb/rig/v3/tool/python"
)

type crontabRecord struct{ lines string }

func (c *crontabRecord) List() (string, error)     { return c.lines, nil }
func (c *crontabRecord) Install(text string) error { c.lines = text; return nil }

func TestDecisionTrainEnqueueLandsTwoMinutesOut(t *testing.T) {
	home := shortTemp(t)
	db, _, _, err := store.Open(filepath.Join(home, "global.sqlite"), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	enqueue := decisionTrainEnqueue(db, home, "/x/rig", t.TempDir(), &crontabRecord{}, func(string) (int, bool) { return 10, true })
	before := time.Now()
	if _, err := enqueue(context.Background(), "reviewer"); err != nil {
		t.Fatal(err)
	}
	var args string
	if err := db.DB.QueryRow(`SELECT args FROM events WHERE op = 'create'`).Scan(&args); err != nil {
		t.Fatal(err)
	}
	var record struct {
		Name  string  `json:"name"`
		At    *string `json:"at"`
		Cron  string  `json:"cron"`
		Stall *int    `json:"stall"`
	}
	if err := json.Unmarshal([]byte(args), &record); err != nil {
		t.Fatal(err)
	}
	if record.Stall == nil || *record.Stall != 10 {
		t.Fatalf("the enqueued train job carries the stall (%v): %s", record.Stall, args)
	}
	if record.At == nil {
		t.Fatalf("the once-job must carry a normalized at: %s", args)
	}
	at, err := time.Parse(time.RFC3339, *record.At)
	if err != nil {
		t.Fatal(err)
	}
	if at.Second() != 0 || at.Nanosecond() != 0 {
		t.Fatalf("the fire must land on an even minute (the crontab's granularity is the margin), got %s", at)
	}
	if margin := at.Sub(before); margin < 2*time.Minute {
		t.Fatalf("the fire must be at least two minutes out, got %s (at %s, before %s)", margin, at, before)
	}
	if margin := at.Sub(before); margin > 3*time.Minute+30*time.Second {
		t.Fatalf("the margin must not drift past the next even minute's bound, got %s", margin)
	}
}

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

var testRiskQuestion = decision.NewQuestion(
	decision.QuestionInput{ID: "risk", Kind: decision.KindChoice, Prompt: "What risk does this bash call carry?"},
	decision.WithChoices("safe", "changes", "dangerous"),
	decision.WithDescription(map[string]string{"safe": "reads", "changes": "writes", "dangerous": "reaches"}),
)

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
STALL = 10
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
STALL = 10
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
	home := shortTemp(t)
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
	return decisionTrain([]string{"train", trainer}, newTrainKernel)
}

func runTrainStderr(t *testing.T, trainer string) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	code := decisionTrain([]string{"train", trainer}, newTrainKernel)
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
	if code := decisionTrain([]string{"train"}, newTrainKernel); code != 2 {
		t.Fatalf("usage exits 2, got %d", code)
	}
	if code := decisionTrain(nil, newTrainKernel); code != 2 {
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

func canonicalCwd(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestDecisionTrainEnqueueLandsALineRunJobFires(t *testing.T) {
	rigHome := t.TempDir()
	schedHome := filepath.Join(rigHome, "scheduler")
	if err := os.MkdirAll(schedHome, 0o755); err != nil {
		t.Fatal(err)
	}
	db, _, _, err := store.Open(filepath.Join(schedHome, "global.sqlite"), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	ct := &crontabRecord{}
	enqueue := decisionTrainEnqueue(db, rigHome, "/x/rig", canonicalCwd(t, "trainfire"), ct, func(string) (int, bool) { return 10, true })
	if _, err := enqueue(context.Background(), "reviewer"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ct.lines, "# rig-scheduler:"+sched.TagHome(rigHome)+":") {
		t.Fatalf("the enqueue must tag its line with the rig home run-job reads, got: %s", ct.lines)
	}
	var id string
	if err := db.DB.QueryRow(`SELECT id FROM jobs`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var spawned [][]string
	spawn := func(_ context.Context, argv []string, _ string, _ []string, _ func([]byte)) (sched.SpawnResult, error) {
		spawned = append(spawned, argv)
		return sched.SpawnResult{Exit: 0}, nil
	}
	fetch := func(string) (json.RawMessage, error) {
		return nil, fmt.Errorf("busy probe must not run")
	}
	if err := sched.RunJob(id, sched.RunOpts{
		Home:    schedHome,
		Crontab: ct,
		Fetch:   fetch,
		Spawn:   spawn,
		RigHome: rigHome,
	}); err != nil {
		t.Fatal(err)
	}
	if len(spawned) != 1 {
		t.Fatalf("run-job must fire the training job, spawned %d times (lines: %s)", len(spawned), ct.lines)
	}
	if !strings.Contains(spawned[0][2], "decision train reviewer") {
		t.Fatalf("the fire must run the training command, got %v", spawned[0])
	}
	var status, reason string
	if err := db.DB.QueryRow(`SELECT status, COALESCE(reason, '') FROM runs WHERE job_id = ?`, id).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status == "skip" {
		t.Fatalf("run-job must not skip the training job for drift, got skip (%s)", reason)
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

type fakeTrainKernel struct {
	runs int
}

const fakeTrainCellMs = 700 * 1000

func (k *fakeTrainKernel) Run(ctx context.Context, code string, timeoutMs int, onStream func(string)) (pythontool.Reply, error) {
	k.runs++
	if strings.Contains(code, "importlib") {
		out := `[{"name":"fake","file":"` + filepath.Join(trainHomeForFake, "train", "fake.py") + `","ok":true,"stall":10}]`
		return pythontool.Reply{Ok: true, Out: &out}, nil
	}
	if timeoutMs != pythontool.NoCellCap {
		return pythontool.Reply{}, fmt.Errorf("fake trainer: the cell runs %dms and the call carried a cap of %dms", fakeTrainCellMs, timeoutMs)
	}
	for _, chunk := range []string{"epoch 1\n", "epoch 2\n"} {
		onStream(chunk)
	}
	trainPath := fakeInvokeArgs(code)
	if trainPath == "" {
		return pythontool.Reply{}, fmt.Errorf("fake trainer: the invoke cell carries no args")
	}
	ckpt := filepath.Join(filepath.Dir(trainPath), "ckpt-fake")
	if strings.Contains(code, `"train"`) {
		if err := os.MkdirAll(ckpt, 0o755); err != nil {
			return pythontool.Reply{}, err
		}
		out := fmt.Sprintf(`{"checkpoint": %q, "questions": {}}`, ckpt)
		return pythontool.Reply{Ok: true, Out: &out}, nil
	}
	if strings.Contains(code, `"evaluate"`) {
		var args []string
		raw := fakeInvokeRaw(code)
		if json.Unmarshal([]byte(raw), &args) != nil || len(args) != 2 {
			return pythontool.Reply{}, fmt.Errorf("fake trainer: the evaluate cell carries %v", args)
		}
		acc := 0.5
		if strings.Contains(args[0], "ckpt-fake") {
			acc = 1.0
		}
		out := fmt.Sprintf(`{"checkpoint": %q, "questions": {"risk": {"n": 2, "correct": 2, "accuracy": %g}}}`, args[0], acc)
		return pythontool.Reply{Ok: true, Out: &out}, nil
	}
	return pythontool.Reply{}, fmt.Errorf("fake trainer: unknown cell %q", cellHead(code))
}

func cellHead(code string) string {
	if len(code) > 60 {
		return code[:60]
	}
	return code
}

var fakeInvokeArgsRe = regexp.MustCompile(`_rig_j\.loads\('(.*?)'\)+$`)

func fakeInvokeRaw(cell string) string {
	m := fakeInvokeArgsRe.FindStringSubmatch(cell)
	if m == nil {
		return "null"
	}
	raw := strings.ReplaceAll(m[1], `\'`, `'`)
	return strings.ReplaceAll(raw, `\\`, `\`)
}

func fakeInvokeArgs(cell string) string {
	var args []string
	if json.Unmarshal([]byte(fakeInvokeRaw(cell)), &args) != nil || len(args) == 0 {
		return ""
	}
	return args[0]
}

var trainHomeForFake string

func (k *fakeTrainKernel) Close() {}

func TestAPrintingTrainerOutlivesTheOldCellCap(t *testing.T) {
	home := trainSetup(t, fakeTrainer, func(home string) string {
		return fmt.Sprintf(`{"trainPython": %q, "decisionUnit": %q}`, kernelInterpreter(t), filepath.Join(home, "laya.service"))
	})
	trainHomeForFake = home
	unit := filepath.Join(home, "laya.service")
	if err := os.WriteFile(unit, []byte(unitTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RIG_HOME", home)
	kernel := &fakeTrainKernel{}
	code, stdout := captureStdout(t, func() int {
		return decisionTrain([]string{"train", "fake"}, func(string, string) (trainKernel, error) { return kernel, nil })
	})
	if code != 0 {
		t.Fatalf("a trainer that prints outlives the old cap: exit %d", code)
	}
	if !strings.Contains(stdout, "epoch 1") || !strings.Contains(stdout, "epoch 2") {
		t.Fatalf("the trainer's progress reaches the job's stream:\n%s", stdout)
	}
	if _, promoted, _ := trainingsRow(t, home); !promoted {
		t.Fatal("the measured win is recorded promoted")
	}
}

func captureStdout(t *testing.T, run func() int) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	code := run()
	os.Stdout = old
	w.Close()
	out, _ := io.ReadAll(r)
	return code, string(out)
}

func TestTheTrainerMenuTracksTheZone(t *testing.T) {
	home := trainSetup(t, fakeTrainer, func(home string) string {
		return fmt.Sprintf(`{"trainPython": %q}`, kernelInterpreter(t))
	})
	menus := newTrainerMenu(home, home)
	if got := menus.names(); len(got) != 1 || got[0] != "fake" {
		t.Fatalf("the zone's trainer lists: %v", got)
	}
	if err := os.WriteFile(filepath.Join(home, "train", "second.py"), []byte(fakeTrainer), 0o644); err != nil {
		t.Fatal(err)
	}
	got := menus.names()
	if len(got) != 2 || got[0] != "fake" || got[1] != "second" {
		t.Fatalf("a trainer dropped into the zone shows without a restart: %v", got)
	}
}

type failingEvaluateKernel struct {
	fakeTrainKernel
}

func (k *failingEvaluateKernel) Run(ctx context.Context, code string, timeoutMs int, onStream func(string)) (pythontool.Reply, error) {
	if strings.Contains(code, "importlib") {
		return k.fakeTrainKernel.Run(ctx, code, timeoutMs, onStream)
	}
	if strings.Contains(code, "evaluate") {
		errText := "the evaluate blew up"
		return pythontool.Reply{Ok: false, Error: &errText}, nil
	}
	return k.fakeTrainKernel.Run(ctx, code, timeoutMs, onStream)
}

func strp(s string) *string { return &s }

func TestAFailedRunKeepsTheExportForARerun(t *testing.T) {
	home := trainSetup(t, fakeTrainer, func(home string) string {
		return fmt.Sprintf(`{"trainPython": %q}`, kernelInterpreter(t))
	})
	trainHomeForFake = home
	t.Setenv("RIG_HOME", home)
	kernel := &failingEvaluateKernel{}
	code, runOut := captureBoth(t, func() int {
		return decisionTrain([]string{"train", "fake"}, func(string, string) (trainKernel, error) { return kernel, nil })
	})
	t.Logf("exit=%d out=%q", code, runOut)
	if code != 1 {
		t.Fatalf("the failing evaluate exits 1, got %d", code)
	}
	runs, err := os.ReadDir(filepath.Join(home, "decision", "train"))
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("a failed run keeps its export (the rows a rerun by hand needs): %v", runs)
	}
	dir := filepath.Join(home, "decision", "train", runs[0].Name())
	rows, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rig-train.jsonl", "rig-heldout.jsonl"} {
		found := false
		for _, r := range rows {
			if r.Name() == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("the export keeps %s: %v", want, rows)
		}
	}
}

func captureBoth(t *testing.T, run func() int) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	code := run()
	os.Stdout, os.Stderr = oldOut, oldErr
	w.Close()
	out, _ := io.ReadAll(r)
	return code, string(out)
}

func TestDecisionTrainEnqueueRefusesATrainerTheZoneDoesNotLoad(t *testing.T) {
	home := shortTemp(t)
	db, _, _, err := store.Open(filepath.Join(home, "global.sqlite"), sched.Statements(), sched.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	ct := &crontabRecord{}
	enqueue := decisionTrainEnqueue(db, home, "/x/rig", t.TempDir(), ct, func(string) (int, bool) { return 0, false })
	if _, err := enqueue(context.Background(), "absent"); err == nil || !strings.Contains(err.Error(), "STALL") {
		t.Fatalf("an unknown trainer is refused, naming the contract: %v", err)
	}
	if ct.lines != "" {
		t.Fatalf("a refused trainer installs no cron line: %q", ct.lines)
	}
}

const stallessTrainer = `def train(rows_path, out_dir):
    return {"checkpoint": out_dir, "questions": {}}
def evaluate(checkpoint, rows_path):
    return {"questions": {}}
`

func TestATrainerWithoutAStallStaysOutOfTheMenu(t *testing.T) {
	home := trainSetup(t, stallessTrainer, func(home string) string {
		return fmt.Sprintf(`{"trainPython": %q}`, kernelInterpreter(t))
	})
	if got := newTrainerMenu(home, home).names(); len(got) != 0 {
		t.Fatalf("a trainer that declares no STALL is skipped: %v", got)
	}
}
