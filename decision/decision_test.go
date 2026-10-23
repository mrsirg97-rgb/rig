package decision_test

import (
	"encoding/json"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/decision"
)

func TestTheQuestionWireKeepsItsBytes(t *testing.T) {
	cases := []struct {
		what string
		q    decision.Question
		want string
	}{
		{"choice", decision.Choice("risk", "risk?", "safe", "changes"), `{"id":"risk","kind":"choice","prompt":"risk?","choices":["safe","changes"]}`},
		{"score", decision.Score("urgency", "how?", "calm", "firm"), `{"id":"urgency","kind":"score","prompt":"how?","criteria":["calm","firm"]}`},
		{"binary", decision.Binary("matter", "matters?"), `{"id":"matter","kind":"binary","prompt":"matters?"}`},
		{"described", decision.NewQuestion(
			decision.QuestionInput{ID: "risk", Kind: decision.KindChoice, Prompt: "risk?"},
			decision.WithChoices("safe"),
			decision.WithDescription(map[string]string{"safe": "reads"}),
		), `{"id":"risk","kind":"choice","prompt":"risk?","choices":["safe"],"description":{"safe":"reads"}}`},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.q)
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if string(got) != c.want {
			t.Fatalf("%s wire = %s, want %s", c.what, got, c.want)
		}
	}
}

func TestParseQuestionNormalizesTheLegacyKind(t *testing.T) {
	q, err := decision.ParseQuestion([]byte(`{"id":"m","kind":"yesno","prompt":"matters?"}`))
	if err != nil {
		t.Fatal(err)
	}
	if q.Kind() != decision.KindBinary || q.ID() != "m" || q.Prompt() != "matters?" {
		t.Fatalf("the legacy yesno reads as binary with its id and prompt: %+v", q)
	}
}

func TestQuestionGettersHandBackCopies(t *testing.T) {
	q := decision.Choice("risk", "risk?", "safe", "changes")
	q.Choices()[0] = "changed"
	if q.Choices()[0] != "safe" {
		t.Fatal("a caller must not move the question's choices under the code that read them")
	}
	described := decision.NewQuestion(
		decision.QuestionInput{ID: "risk", Kind: decision.KindChoice, Prompt: "risk?"},
		decision.WithDescription(map[string]string{"safe": "reads"}),
	)
	described.Description()["safe"] = "changed"
	if described.Description()["safe"] != "reads" {
		t.Fatal("a caller must not move the question's description under the code that read it")
	}
}
