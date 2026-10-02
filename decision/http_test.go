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

func TestTheClientSpeaksTheWire(t *testing.T) {
	var got struct {
		State     string              `json:"state"`
		Questions []decision.Question `json:"questions"`
	}
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			t.Fatalf("the endpoint is /v1/systemone, got %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":[{"question":"risk","value":"safe","confidence":0.71}]}`))
	}))
	dec, err := decision.NewHTTP(decision.HTTPOptions{URL: srv.URL, Client: testClient(), Decider: "laya"})
	if err != nil {
		t.Fatal(err)
	}
	answers, err := dec.Decide(context.Background(), `{"command":"ls"}`, []decision.Question{
		decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 1 || answers[0].Value != "safe" || answers[0].Confidence != 0.71 {
		t.Fatalf("the answers carry the value and the probability: %+v", answers)
	}
	if answers[0].Decider != "laya" {
		t.Fatalf("the answer names its decider: %+v", answers[0])
	}
	if got.State != `{"command":"ls"}` {
		t.Fatalf("the state rides the request: %q", got.State)
	}
	if len(got.Questions) != 1 || got.Questions[0].Kind != decision.KindChoice || len(got.Questions[0].Choices) != 3 {
		t.Fatalf("the typed questions ride the request: %+v", got.Questions)
	}
}

func TestTheDeciderDefaultsToTheHost(t *testing.T) {
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers":[{"question":"risk","value":"safe","confidence":0.9}]}`))
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

func TestAnOutOfRangeConfidenceIsDropped(t *testing.T) {
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers":[
			{"question":"risk","value":"safe","confidence":1.5},
			{"question":"risk","value":"changes","confidence":0.4}]}`))
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

func TestAnUnreachableServerIsAnError(t *testing.T) {
	dec, err := decision.NewHTTP(decision.HTTPOptions{URL: "http://127.0.0.1:1", Client: testClient(), Decider: "laya"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decide(context.Background(), "s", []decision.Question{decision.YesNo("ok", "ok?")}); err == nil {
		t.Fatal("an unreachable decider is an error, never a silent answer")
	}
}
