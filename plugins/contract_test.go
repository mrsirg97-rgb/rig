package plugins

import (
	"strings"
	"testing"
)

func TestTheTrainerContractRefusesAMissingEvaluate(t *testing.T) {
	trainOnly := "def train(rows_path, out_dir):\n    return {}\n"
	_, _, err := WritePending(t.TempDir(), "train", nil, "laya", trainOnly, TrainerContract)
	if err == nil || !strings.Contains(err.Error(), "missing def evaluate(") {
		t.Fatalf("a file with no evaluate refuses, got %v", err)
	}
	_, _, err = WritePending(t.TempDir(), "train", nil, "laya", "x = 1\n", TrainerContract)
	if err == nil || !strings.Contains(err.Error(), "missing def train(") {
		t.Fatalf("a file with neither method refuses naming train, got %v", err)
	}
	both := trainOnly + "def evaluate(checkpoint, rows_path):\n    return {}\n"
	path, created, err := WritePending(t.TempDir(), "train", nil, "laya", both, TrainerContract)
	if err != nil || !created || path == "" {
		t.Fatalf("a trainer carrying both methods writes: %v, %v", err, path)
	}
}

func TestTheZoneNamesTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	files, err := Zone(home, "train", "pending")
	if err != nil || files != nil {
		t.Fatalf("an absent zone reads empty: %v, %v", files, err)
	}
	listed, err := List(home, "train")
	if err != nil || listed != nil {
		t.Fatalf("an absent listing reads empty: %v, %v", listed, err)
	}
	if _, _, err := WritePending(home, "train", nil, "laya", "def train(rows_path, out_dir):\n    return {}\ndef evaluate(checkpoint, rows_path):\n    return {}\n", TrainerContract); err != nil {
		t.Fatal(err)
	}
	pending, err := Zone(home, "train", "pending")
	if err != nil || len(pending) != 1 {
		t.Fatalf("the pending zone of train/ carries the trainer: %v, %v", pending, err)
	}
}
