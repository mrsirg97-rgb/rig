package decision_test

import (
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/decision"
)

func strPtr(s string) *string { return &s }

func TestParseReviewerLabelReadsEveryLiveRationale(t *testing.T) {
	for i, r := range liveRationales {
		label, ok := decision.ParseReviewerLabel(r.text)
		if !ok || label != r.label {
			t.Fatalf("rationale %d read (%q, %v), want %q", i, label, ok, r.label)
		}
	}
	if _, ok := decision.ParseReviewerLabel("I could not run it"); ok {
		t.Fatal("a rationale with no label must not parse")
	}
}

func goldChoice(id int64, answer string, corrected *string, status string) decision.GoldRow {
	return decision.GoldRow{
		ID: id, State: `{"command":"ls"}`,
		Question: decision.Question{
			ID: "risk", Kind: decision.KindChoice, Prompt: "What risk does this bash call carry?",
			Choices:     []string{"safe", "changes", "dangerous"},
			Description: map[string]string{"safe": "reads", "changes": "writes", "dangerous": "reaches"},
		},
		Answer: answer, Corrected: corrected, Status: status,
	}
}

func goldBinary(id int64, answer string, corrected *string, status string) decision.GoldRow {
	return decision.GoldRow{
		ID: id, State: `{"task":"x"}`,
		Question: decision.Binary("matter", "Does this symbol matter for the task?"),
		Answer:   answer, Corrected: corrected, Status: status,
	}
}

func TestGoldRowsTakesTheApprovedAnswerAndTheDeniedCorrection(t *testing.T) {
	set, err := decision.GoldRows([]decision.GoldRow{
		goldChoice(7, "safe", nil, decision.StatusApproved),
		goldChoice(9, "changes", strPtr("Corrected answer: dangerous. it deletes"), decision.StatusDenied),
		goldBinary(11, "yes", strPtr("no \u2014 a test helper does not matter"), decision.StatusDenied),
		goldBinary(13, "yes", nil, decision.StatusApproved),
		goldChoice(15, "safe", strPtr("I could not run it"), decision.StatusDenied),
		{ID: 17, State: "{}", Question: decision.Score("s", "rate", "a", "b"), Answer: "3", Status: decision.StatusApproved},
		goldChoice(19, "safe", strPtr("the label is destructive"), decision.StatusDenied),
	})
	if err != nil {
		t.Fatal(err)
	}
	if set.Skipped != 3 {
		t.Fatalf("skipped = %d, want 3 (unparseable, score, unknown label)", set.Skipped)
	}
	if len(set.Rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(set.Rows))
	}
	if set.Rows[0].Expected["risk"] != "safe" {
		t.Fatalf("an approved row takes the proposer's answer: %v", set.Rows[0].Expected)
	}
	if set.Rows[1].Expected["risk"] != "dangerous" {
		t.Fatalf("a denied row takes the parsed correction: %v", set.Rows[1].Expected)
	}
	if set.Rows[2].Expected["matter"] != false {
		t.Fatalf("a denied noul row reads its correction: %v", set.Rows[2].Expected)
	}
	if set.Rows[3].Expected["matter"] != true {
		t.Fatalf("an approved noul row keeps the answer: %v", set.Rows[3].Expected)
	}
	if set.Rows[0].Questions["risk"].Type != "choice" || set.Rows[2].Questions["matter"].Type != "noul" {
		t.Fatalf("the laya types are wrong: %v %v", set.Rows[0].Questions, set.Rows[2].Questions)
	}
	if set.Rows[0].Questions["risk"].Criteria["safe"] == "" {
		t.Fatalf("a choice carries its label meanings: %v", set.Rows[0].Questions["risk"])
	}
	if set.Rows[2].Questions["matter"].Criteria != nil {
		t.Fatalf("a noul carries no criteria: %v", set.Rows[2].Questions["matter"])
	}
}

func TestGoldRowsRefusesARowWithNoQuestionID(t *testing.T) {
	_, err := decision.GoldRows([]decision.GoldRow{{ID: 1, State: "{}", Question: decision.Question{}, Answer: "safe", Status: decision.StatusApproved}})
	if err == nil || !strings.Contains(err.Error(), "no question id") {
		t.Fatalf("a row with no question id is a store bug and refuses loudly, got %v", err)
	}
}

func TestSplitHoldsEveryFifthOfEachGroupAndRepeats(t *testing.T) {
	rows := make([]decision.LayaRow, 0, 30)
	for i := 1; i <= 20; i++ {
		rows = append(rows, layaRow(int64(i), "risk", "safe"))
	}
	for i := 21; i <= 30; i++ {
		rows = append(rows, layaRow(int64(i), "risk", "dangerous"))
	}
	train, held := decision.Split(rows)
	if len(held) != 6 || len(train) != 24 {
		t.Fatalf("split = %d held / %d train, want 6/24", len(held), len(train))
	}
	for _, r := range held {
		if r.ID%5 != 1 {
			t.Fatalf("held row %d is not every fifth of its group", r.ID)
		}
	}
	safeHeld, dangerousHeld := 0, 0
	for _, r := range held {
		if r.Expected["risk"] == "safe" {
			safeHeld++
		} else {
			dangerousHeld++
		}
	}
	if safeHeld != 4 || dangerousHeld != 2 {
		t.Fatalf("the split is not stratified: %d safe / %d dangerous held", safeHeld, dangerousHeld)
	}
	train2, held2 := decision.Split(rows)
	if !sameRows(train, train2) || !sameRows(held, held2) {
		t.Fatal("the same rows must split the same way")
	}
}

func TestSplitStratifiesByQuestionAndLabel(t *testing.T) {
	rows := []decision.LayaRow{
		layaRow(1, "risk", "safe"), layaRow(2, "risk", "safe"), layaRow(3, "risk", "changes"),
		layaRow(4, "matter", true), layaRow(5, "matter", false), layaRow(6, "matter", true),
		layaRow(7, "matter", true), layaRow(8, "matter", true), layaRow(9, "matter", false),
		layaRow(10, "matter", true),
	}
	train, held := decision.Split(rows)
	if len(held) != 4 || len(train) != 6 {
		t.Fatalf("split = %d held / %d train, want 4/6 (one per group)", len(held), len(train))
	}
	ids := map[int64]bool{}
	for _, r := range held {
		ids[r.ID] = true
	}
	for _, want := range []int64{1, 3, 4, 5} {
		if !ids[want] {
			t.Fatalf("each question-and-label group holds its own fifth: %v", ids)
		}
	}
}

func sameRows(a, b []decision.LayaRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}

func layaRow(id int64, question string, expected any) decision.LayaRow {
	kind := "choice"
	if question == "matter" {
		kind = "noul"
	}
	return decision.LayaRow{
		ID:    id,
		State: `{"x":1}`,
		Questions: map[string]decision.LayaQuestion{
			question: {Type: kind, Instructions: "q"},
		},
		Expected: map[string]any{question: expected},
	}
}

func TestConstantReportIsComputedFromTheTrainRows(t *testing.T) {
	train := []decision.LayaRow{
		layaRow(1, "risk", "safe"), layaRow(2, "risk", "safe"), layaRow(3, "risk", "safe"),
		layaRow(4, "risk", "safe"), layaRow(5, "risk", "dangerous"), layaRow(6, "risk", "dangerous"),
		layaRow(7, "matter", true), layaRow(8, "matter", true), layaRow(9, "matter", true),
		layaRow(10, "matter", true), layaRow(11, "matter", true), layaRow(12, "matter", true),
		layaRow(13, "matter", false), layaRow(14, "matter", false), layaRow(15, "matter", false),
	}
	held := []decision.LayaRow{
		layaRow(16, "risk", "safe"), layaRow(17, "risk", "dangerous"), layaRow(18, "risk", "safe"),
		layaRow(19, "matter", true), layaRow(20, "matter", false), layaRow(21, "matter", false),
	}
	got := decision.ConstantReport(train, held)
	risk := got.Questions["risk"]
	if risk.N != 3 || risk.Correct != 2 || risk.Accuracy != 2.0/3.0 {
		t.Fatalf("the constant risk report = %+v, want the majority label scored on held-out", risk)
	}
	matter := got.Questions["matter"]
	if matter.N != 3 || matter.Correct != 1 {
		t.Fatalf("the constant matter report = %+v, want the majority class scored on held-out", matter)
	}
	if matter.PTrue == nil || matter.PTrue.GoldTrue != 2.0/3.0 || matter.PTrue.GoldFalse != 2.0/3.0 {
		t.Fatalf("a constant's p(true) is the train prior on both gold classes: %+v", matter.PTrue)
	}
	if risk.PTrue != nil {
		t.Fatal("a choice question carries no p(true)")
	}
}

func TestBeatsNeedsEveryQuestionStrictlyOverBoth(t *testing.T) {
	candidate := decision.Report{Questions: map[string]decision.QuestionReport{
		"risk": {N: 10, Correct: 9, Accuracy: 0.9},
	}}
	constant := decision.Report{Questions: map[string]decision.QuestionReport{
		"risk": {N: 10, Correct: 6, Accuracy: 0.6},
	}}
	incumbent := decision.Report{Questions: map[string]decision.QuestionReport{
		"risk": {N: 10, Correct: 8, Accuracy: 0.8},
	}}
	if !decision.Beats(candidate, constant, incumbent) {
		t.Fatal("a strict win over both on every question promotes")
	}
	if decision.Beats(constant, constant, incumbent) {
		t.Fatal("a tie with the constant is not a beat")
	}
	if decision.Beats(incumbent, constant, incumbent) {
		t.Fatal("a tie with the incumbent is not a beat")
	}
	worse := decision.Report{Questions: map[string]decision.QuestionReport{
		"risk": {N: 10, Correct: 5, Accuracy: 0.5},
	}}
	if decision.Beats(worse, constant, incumbent) {
		t.Fatal("a loss promotes nothing")
	}
	missing := decision.Report{Questions: map[string]decision.QuestionReport{
		"risk": {N: 10, Correct: 9, Accuracy: 0.9}, "matter": {N: 5, Correct: 5, Accuracy: 1},
	}}
	if decision.Beats(missing, constant, incumbent) {
		t.Fatal("a question missing from a report is not a beat")
	}
	if decision.Beats(decision.Report{Questions: map[string]decision.QuestionReport{}}, constant, incumbent) {
		t.Fatal("an empty report promotes nothing")
	}
}

func TestParseReportRefusesNonsense(t *testing.T) {
	if _, err := decision.ParseReport(`{"questions":{}}`); err != nil {
		t.Fatalf("a train report may carry no questions yet: %v", err)
	}
	if _, err := decision.ParseReport(`{"questions":{"risk":{"n":0,"correct":0,"accuracy":0}}}`); err == nil {
		t.Fatal("a question with no rows refuses")
	}
	if _, err := decision.ParseReport(`{"questions":{"risk":{"n":10,"correct":8,"accuracy":81}}}`); err == nil {
		t.Fatal("a percentage is not an accuracy")
	}
	if _, err := decision.ParseReport(`{"questions":{"matter":{"n":10,"correct":8,"accuracy":0.8,"pTrue":{"goldTrue":1.5,"goldFalse":0.03}}}}`); err == nil {
		t.Fatal("a p(true) outside the probability range refuses")
	}
	good, err := decision.ParseReport(`{"checkpoint":"/tmp/x","questions":{"risk":{"n":10,"correct":8,"accuracy":0.8}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if good.Checkpoint != "/tmp/x" || good.Questions["risk"].Accuracy != 0.8 {
		t.Fatalf("a good report parses: %+v", good)
	}
}
