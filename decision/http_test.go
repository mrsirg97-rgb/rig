package decision_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/testenv"
)

func testClient() *http.Client {
	return &http.Client{Transport: testenv.Transport()}
}

// layaReply is Laya's /v1/systemone payload in its full shape: the answers
// are keyed by question id, each answer carries its type, its value, the
// probabilities, the entropy confidence beside the answer_confidence, and
// the action block; the envelope carries the model and the usage and the
// routing a client ignores. The decoder must take all of it.
const layaChoiceReply = `{
	"model": "laya-rl-agent",
	"answers": {
		"risk": {"type": "choice", "choice": "safe",
		         "probabilities": {"safe": 0.71, "changes": 0.2, "dangerous": 0.09},
		         "confidence": 0.55, "answer_confidence": 0.71,
		         "action": {"act_probability": 1.0}}
	},
	"usage": {"input_tokens": 83, "output_tokens": 0, "state_tokens": 12,
	          "state_tokens_dropped": 0, "truncated": false, "truncated_questions": []},
	"routing": {"model": "english", "repo": "convaiinnovations/laya", "reason": "English Latin text"}
}`

func TestTheClientSpeaksLayasWire(t *testing.T) {
	var got struct {
		State     string `json:"state"`
		Questions map[string]struct {
			Type         string            `json:"type"`
			Instructions string            `json:"instructions"`
			Criteria     map[string]string `json:"criteria"`
		} `json:"questions"`
	}
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			t.Fatalf("the endpoint is /v1/systemone, got %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(layaChoiceReply))
	}))
	dec, err := decision.NewHTTP(decision.HTTPOptions{URL: srv.URL, Client: testClient(), Decider: "laya"})
	if err != nil {
		t.Fatal(err)
	}
	risk := decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous")
	risk.Description = map[string]string{
		"safe":      "reads or lists; nothing on disk changes",
		"changes":   "writes only inside the workspace it named",
		"dangerous": "reaches outside the workspace, deletes, or can destroy state",
	}
	answers, err := dec.Decide(context.Background(), `{"command":"ls"}`, []decision.Question{risk})
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 1 || answers[0].Value != "safe" || answers[0].Confidence != 0.71 {
		t.Fatalf("the answer is the chosen label and the answer_confidence, not the entropy confidence: %+v", answers)
	}
	if answers[0].Decider != "laya" {
		t.Fatalf("the answer names its decider: %+v", answers[0])
	}
	if answers[0].Question != "risk" {
		t.Fatalf("the answer names its question: %+v", answers[0])
	}
	if got.State != `{"command":"ls"}` {
		t.Fatalf("the state rides the request: %q", got.State)
	}
	q, ok := got.Questions["risk"]
	if !ok {
		t.Fatalf("the questions are keyed by id: %v", got.Questions)
	}
	if q.Type != "choice" || q.Instructions != "What risk does this bash call carry?" {
		t.Fatalf("the question speaks Laya's shape: %+v", q)
	}
	want := map[string]string{
		"safe":      "reads or lists; nothing on disk changes",
		"changes":   "writes only inside the workspace it named",
		"dangerous": "reaches outside the workspace, deletes, or can destroy state",
	}
	if len(q.Criteria) != 3 || q.Criteria["safe"] != want["safe"] || q.Criteria["changes"] != want["changes"] || q.Criteria["dangerous"] != want["dangerous"] {
		t.Fatalf("the choice describes each label on the wire: %+v", q.Criteria)
	}
}

func TestAYesNoQuestionIsNoul(t *testing.T) {
	var got struct {
		Questions map[string]struct {
			Type         string            `json:"type"`
			Instructions string            `json:"instructions"`
			Criteria     map[string]string `json:"criteria"`
		} `json:"questions"`
	}
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"answers": {"ok": {"type": "noul", "noul": 0.87, "confidence": 0.87, "answer_confidence": 0.87}}, "usage": {}}`))
	}))
	dec, err := decision.NewHTTP(decision.HTTPOptions{URL: srv.URL, Client: testClient(), Decider: "laya"})
	if err != nil {
		t.Fatal(err)
	}
	answers, err := dec.Decide(context.Background(), "s", []decision.Question{decision.YesNo("ok", "ok?")})
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 1 || answers[0].Value != "yes" || answers[0].Confidence != 0.87 {
		t.Fatalf("the noul answer is the side the probability names: %+v", answers)
	}
	q, ok := got.Questions["ok"]
	if !ok || q.Type != "noul" || q.Instructions != "ok?" {
		t.Fatalf("the yes/no question rides as a noul: %+v", q)
	}
	if len(q.Criteria) != 0 {
		t.Fatalf("a noul carries no criteria: %+v", q.Criteria)
	}
}

func TestANoulBelowHalfIsNo(t *testing.T) {
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers": {"ok": {"type": "noul", "noul": 0.4, "answer_confidence": 0.6}}}`))
	}))
	dec, err := decision.NewHTTP(decision.HTTPOptions{URL: srv.URL, Client: testClient(), Decider: "laya"})
	if err != nil {
		t.Fatal(err)
	}
	answers, err := dec.Decide(context.Background(), "s", []decision.Question{decision.YesNo("ok", "ok?")})
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 1 || answers[0].Value != "no" || answers[0].Confidence != 0.6 {
		t.Fatalf("the noul answer at 0.4 is no, and the confidence is the mass on that side: %+v", answers)
	}
}

func TestAScoreQuestionTakesACriteriaList(t *testing.T) {
	var got struct {
		Questions map[string]struct {
			Type         string   `json:"type"`
			Instructions string   `json:"instructions"`
			Criteria     []string `json:"criteria"`
		} `json:"questions"`
	}
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"answers": {"urgency": {"type": "score", "score": 1.7, "legend": {"1": "firm", "2": "angry"}, "probabilities": {"1": 0.5, "2": 0.3}, "answer_confidence": 0.5}}}`))
	}))
	dec, err := decision.NewHTTP(decision.HTTPOptions{URL: srv.URL, Client: testClient(), Decider: "laya"})
	if err != nil {
		t.Fatal(err)
	}
	answers, err := dec.Decide(context.Background(), "s", []decision.Question{
		decision.Score("urgency", "How urgent?", "calm", "firm", "angry", "furious"),
	})
	if err != nil {
		t.Fatal(err)
	}
	q, ok := got.Questions["urgency"]
	if !ok || q.Type != "score" || q.Instructions != "How urgent?" {
		t.Fatalf("the score question rides as a score: %+v", q)
	}
	if len(q.Criteria) != 4 || q.Criteria[0] != "calm" || q.Criteria[3] != "furious" {
		t.Fatalf("the score carries its criteria list in order: %+v", q.Criteria)
	}
	if len(answers) != 1 || answers[0].Value != "1.7" || answers[0].Confidence != 0.5 {
		t.Fatalf("the score answer is the expected level: %+v", answers)
	}
}

func TestAnUnknownAnswerTypeIsNoAnswer(t *testing.T) {
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers": {"risk": {"type": "essay", "choice": "safe", "answer_confidence": 0.9}}}`))
	}))
	dec, err := decision.NewHTTP(decision.HTTPOptions{URL: srv.URL, Client: testClient(), Decider: "laya"})
	if err != nil {
		t.Fatal(err)
	}
	answers, err := dec.Decide(context.Background(), "s", []decision.Question{
		decision.Choice("risk", "risk?", "safe", "changes", "dangerous"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 0 {
		t.Fatalf("an answer of no known type is no answer: %+v", answers)
	}
}

func TestAnOutOfRangeAnswerConfidenceIsDropped(t *testing.T) {
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers": {
			"risk": {"type": "choice", "choice": "safe", "answer_confidence": 1.5},
			"other": {"type": "choice", "choice": "changes", "answer_confidence": 0.4}}}`))
	}))
	dec, err := decision.NewHTTP(decision.HTTPOptions{URL: srv.URL, Client: testClient(), Decider: "laya"})
	if err != nil {
		t.Fatal(err)
	}
	answers, err := dec.Decide(context.Background(), "s", []decision.Question{decision.Choice("risk", "risk?", "safe", "changes", "dangerous")})
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 1 || answers[0].Value != "changes" {
		t.Fatalf("the out-of-range confidence drops, the valid answer stays: %+v", answers)
	}
}

func TestTheDeciderDefaultsToTheHost(t *testing.T) {
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers": {"ok": {"type": "noul", "noul": 0.9, "answer_confidence": 0.9}}}`))
	}))
	dec, err := decision.NewHTTP(decision.HTTPOptions{URL: srv.URL, Client: testClient()})
	if err != nil {
		t.Fatal(err)
	}
	answers, err := dec.Decide(context.Background(), "s", []decision.Question{decision.YesNo("ok", "ok?")})
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 1 || answers[0].Decider != strings.TrimPrefix(srv.URL, "http://") {
		t.Fatalf("the decider is the host: %+v", answers[0])
	}
}

func TestAnUnreachableServerIsAnError(t *testing.T) {
	dec, err := decision.NewHTTP(decision.HTTPOptions{URL: "http://127.0.0.1:1", Client: testClient(), Decider: "laya"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decide(context.Background(), "s", []decision.Question{decision.YesNo("ok", "ok?")}); err == nil {
		t.Fatal("an unreachable decider is an error, never a silent answer")
	}
}
