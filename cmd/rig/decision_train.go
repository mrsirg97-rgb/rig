package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v2"
	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/plugins"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
	sched "github.com/mrsirg97-rgb/rig/v2/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
	pythontool "github.com/mrsirg97-rgb/rig/v2/tool/python"
)

const decisionTrainUsage = "decision: usage: rig decision train <trainer>"

func decisionTrain(args []string) int {
	if len(args) != 2 || args[0] != "train" {
		fmt.Fprintln(os.Stderr, decisionTrainUsage)
		return 2
	}
	trainer := args[1]
	if !plugins.PluginNameRe.MatchString(trainer) {
		fmt.Fprintf(os.Stderr, "rig: decision train: %q is not a trainer name (the filename stem)\n", trainer)
		return 2
	}
	cfgDir, cwd, cfg, err := boot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 1
	}
	trainPython := cfg.Settings.TrainPython
	if trainPython == "" {
		fmt.Fprintln(os.Stderr, "rig: decision train: no trainer interpreter: set trainPython in settings.json (the train zone's own venv; torch is two gigabytes)")
		return 1
	}
	unitPath := cfg.Settings.DecisionUnit

	decPath := decisionstore.FilePath(cfgDir)
	if err := os.MkdirAll(filepath.Dir(decPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		return 1
	}
	decdb, _, _, err := store.Open(decPath, decisionstore.Statements(), decisionstore.SchemaVersion, decisionstore.Migration())
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train: decision store:", err)
		return 1
	}
	defer decdb.DB.Close()

	ctx := context.Background()
	engine, room := newFleet()
	go engine.Start(ctx)
	defer engine.Stop()
	noticePrinter(ctx, room)
	voice := room.Add(rig.MemberDecision)

	k := pythontool.NewWith(trainPython, pythontool.DefaultHost(), cwd)
	defer k.Close()

	if err := os.MkdirAll(filepath.Join(cfgDir, "train", "pending"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		return 1
	}
	files, err := plugins.List(cfgDir, "train")
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		return 1
	}
	reports, err := plugins.DiscoverChecked(ctx, k, files, nil, plugins.TrainerContract)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		return 1
	}
	loaded := make([]string, 0, len(reports))
	var found *plugins.Report
	for i := range reports {
		if reports[i].Skipped {
			continue
		}
		loaded = append(loaded, reports[i].Name)
		if reports[i].Name == trainer {
			found = &reports[i]
		}
	}
	if found == nil {
		for _, rep := range reports {
			if rep.Name == trainer && rep.Skipped {
				fmt.Fprintf(os.Stderr, "rig: decision train: %s: %s\n", trainer, rep.Reason)
				return 1
			}
		}
		fmt.Fprintf(os.Stderr, "rig: decision train: no trainer %q in %s (the zone carries: %s)\n", trainer, filepath.Join(cfgDir, "train"), strings.Join(loaded, ", "))
		return 1
	}

	gold, err := decisionstore.Gold(ctx, decdb)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		return 1
	}
	set, err := decision.GoldRows(gold)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		return 1
	}
	trainRows, heldRows := decision.Split(set.Rows)
	if len(trainRows) == 0 || len(heldRows) == 0 {
		fmt.Fprintf(os.Stderr, "rig: decision train: the settled rows do not split (train %d, held %d)\n", len(trainRows), len(heldRows))
		return 1
	}
	constant := decision.ConstantReport(trainRows, heldRows)

	tag := time.Now().UTC().Format("20060102-1504")
	runDir := filepath.Join(cfgDir, "decision", "train", tag)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		return 1
	}
	trainPath := filepath.Join(runDir, "rig-train.jsonl")
	heldPath := filepath.Join(runDir, "rig-heldout.jsonl")
	if err := writeJSONL(trainPath, trainRows); err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		return 1
	}
	if err := writeJSONL(heldPath, heldRows); err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		return 1
	}

	kind := plugins.TrainerContract.Kind
	checkpointRaw, err := plugins.Invoke(ctx, k, kind, trainer, "train", pythontool.MaxCellMs, trainPath, runDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		return 1
	}
	trainReport, err := decision.ParseReport(string(checkpointRaw))
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		return 1
	}
	checkpoint := trainReport.Checkpoint
	if checkpoint == "" {
		fmt.Fprintln(os.Stderr, "rig: decision train: the trainer named no checkpoint")
		return 1
	}
	if _, err := os.Stat(checkpoint); err != nil {
		fmt.Fprintf(os.Stderr, "rig: decision train: the trainer's checkpoint %s is absent: %v\n", checkpoint, err)
		return 1
	}

	candidateRaw, err := plugins.Invoke(ctx, k, kind, trainer, "evaluate", pythontool.MaxCellMs, checkpoint, heldPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		return 1
	}
	candidate, err := decision.ParseReport(string(candidateRaw))
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		return 1
	}
	if len(candidate.Questions) == 0 {
		fmt.Fprintln(os.Stderr, "rig: decision train: the trainer's evaluate carries no questions")
		return 1
	}

	incumbentJSON := ""
	var incumbent decision.Report
	if unitPath != "" {
		incumbentPath, err := unitCheckpoint(unitPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rig: decision train:", err)
			return 1
		}
		incumbentRaw, err := plugins.Invoke(ctx, k, kind, trainer, "evaluate", pythontool.MaxCellMs, incumbentPath, heldPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rig: decision train:", err)
			return 1
		}
		incumbent, err = decision.ParseReport(string(incumbentRaw))
		if err != nil {
			fmt.Fprintln(os.Stderr, "rig: decision train:", err)
			return 1
		}
		if len(incumbent.Questions) == 0 {
			fmt.Fprintln(os.Stderr, "rig: decision train: the incumbent's evaluate carries no questions")
			return 1
		}
		incumbentJSON = string(incumbentRaw)
	}

	promoted := unitPath != "" && decision.Beats(candidate, constant, incumbent)
	promoteLine := ""
	failExit := false
	if promoted {
		promoteLine, err = rewriteUnit(unitPath, checkpoint)
		if err != nil {
			promoted = false
			failExit = true
			fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		}
	}

	if _, err := decisionstore.RecordTraining(ctx, decdb, decisionstore.TrainingInput{
		Scope: scope.Key(cwd), Trainer: trainer, Rows: len(gold), Skipped: set.Skipped,
		TrainRows: len(trainRows), HeldRows: len(heldRows), RunDir: runDir, Checkpoint: checkpoint,
		Constant: marshalReport(constant), Incumbent: incumbentJSON, Candidate: string(candidateRaw),
		Promoted: promoted,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "rig: decision train:", err)
		return 1
	}

	table := trainTable(trainer, len(gold), set.Skipped, len(trainRows), len(heldRows), constant, incumbent, candidate, promoted, promoteLine)
	broadcast.Say(voice, "decision", table, core.LevelInfo)
	fmt.Println(table)

	if failExit {
		return 1
	}
	return 0
}

func writeJSONL(path string, rows []decision.LayaRow) error {
	var b strings.Builder
	for _, r := range rows {
		line, err := json.Marshal(r)
		if err != nil {
			return fmt.Errorf("decision train: row %d: %v", r.ID, err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func marshalReport(r decision.Report) string {
	b, err := json.Marshal(r)
	if err != nil {
		return "{}"
	}
	return string(b)
}

var checkpointValueRe = regexp.MustCompile(`RIG_DECISION_CHECKPOINT=\S+`)

func unitCheckpoint(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("decision train: read %s: %v", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Environment=") {
			continue
		}
		if m := checkpointValueRe.FindStringSubmatch(line); m != nil {
			return expandUnitValue(strings.TrimPrefix(m[0], "RIG_DECISION_CHECKPOINT=")), nil
		}
	}
	return "", fmt.Errorf("decision train: no RIG_DECISION_CHECKPOINT in %s (nothing is served to beat)", path)
}

func expandUnitValue(v string) string {
	if strings.Contains(v, "%h") {
		home, err := os.UserHomeDir()
		if err != nil {
			return v
		}
		v = strings.ReplaceAll(v, "%h", home)
	}
	if !filepath.IsAbs(v) {
		home, err := os.UserHomeDir()
		if err != nil {
			return v
		}
		v = filepath.Join(home, v)
	}
	return v
}

func rewriteUnit(path, checkpoint string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("decision train: read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		if !strings.HasPrefix(line, "Environment=") {
			continue
		}
		if checkpointValueRe.MatchString(line) {
			lines[i] = checkpointValueRe.ReplaceAllString(line, "RIG_DECISION_CHECKPOINT="+checkpoint)
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("decision train: no RIG_DECISION_CHECKPOINT in %s", path)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return "", fmt.Errorf("decision train: write %s: %v", path, err)
	}
	unit := strings.TrimSuffix(filepath.Base(path), ".service")
	return fmt.Sprintf("the operator runs: systemctl --user daemon-reload && systemctl --user restart %s", unit), nil
}

func trainTable(trainer string, gold, skipped, trainN, heldN int, constant, incumbent, candidate decision.Report, promoted bool, promoteLine string) string {
	var b strings.Builder
	word := "not promoted"
	if promoted {
		word = "promoted"
	}
	fmt.Fprintf(&b, "decision train %s \u00b7 %d gold (%d skipped) \u00b7 %d train / %d held \u00b7 %s", trainer, gold, skipped, trainN, heldN, word)
	for _, id := range reportIDs(candidate) {
		row := "\n" + id + "  candidate " + pct(candidate.Questions[id].Accuracy) + "  constant"
		if q, ok := constant.Questions[id]; ok {
			row += " " + pct(q.Accuracy)
		} else {
			row += " \u2014"
		}
		if q, ok := incumbent.Questions[id]; ok {
			row += "  incumbent " + pct(q.Accuracy)
		} else {
			row += "  incumbent \u2014"
		}
		if q := candidate.Questions[id].PTrue; q != nil {
			row += fmt.Sprintf("  p(true) gold-yes %.2f gold-no %.2f", q.GoldTrue, q.GoldFalse)
		}
		b.WriteString(row)
	}
	if promoteLine != "" {
		b.WriteString("\n" + promoteLine)
	}
	return b.String()
}

func reportIDs(r decision.Report) []string {
	ids := make([]string, 0, len(r.Questions))
	for id := range r.Questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func pct(a float64) string {
	return fmt.Sprintf("%.0f%%", 100*a)
}

func decisionTrainEnqueue(db store.DB, rigHome, self, cwd string, ct sched.Crontab) func(context.Context, string) (string, error) {
	return func(ctx context.Context, trainer string) (string, error) {
		if !plugins.PluginNameRe.MatchString(trainer) {
			return "", fmt.Errorf("decision: %q is not a trainer name (the filename stem)", trainer)
		}
		session := ""
		if s, ok := core.SessionFrom(ctx); ok {
			session = s.ID
		}
		now := time.Now()
		m := now.Add(2 * time.Minute)
		at := m.Truncate(time.Minute)
		if at.Before(m) {
			at = at.Add(time.Minute)
		}
		tag := now.UTC().Format("20060102-1504")
		return sched.Create(ctx, db, ct, sched.CreateInput{
			Name:    "decision-train-" + trainer + "-" + tag,
			Command: self + " decision train " + trainer,
			Cron:    "once",
			At:      at.UTC().Format(time.RFC3339),
			Cwd:     cwd,
		}, cwd, session, self+" run-job", rigHome, time.Now)
	}
}
