package decision_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
	"github.com/mrsirg97-rgb/rig/v2/testenv"
	"github.com/mrsirg97-rgb/rig/v2/tool"
	filetool "github.com/mrsirg97-rgb/rig/v2/tool/file"
)

type probe struct {
	mu          sync.Mutex
	states      []string
	questions   []map[string]json.RawMessage
	requests    int
	inFlight    int
	maxInFlight int
	delay       time.Duration
}

func (p *probe) snapshot() (int, []string, []map[string]json.RawMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests, append([]string(nil), p.states...), append([]map[string]json.RawMessage(nil), p.questions...)
}

func (p *probe) maxConcurrent() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxInFlight
}

func wireReply(answers map[string]any) string {
	b, err := json.Marshal(map[string]any{
		"model":   "laya-rl-agent",
		"answers": answers,
		"usage":   map[string]any{"input_tokens": 83, "output_tokens": 0, "state_tokens": 12, "state_tokens_dropped": 0, "truncated": false, "truncated_questions": []string{}},
		"routing": map[string]any{"model": "english", "repo": "convaiinnovations/laya", "reason": "English Latin text"},
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func choiceReply(choice string, probabilities map[string]float64) string {
	return wireReply(map[string]any{
		"item": map[string]any{
			"type":          "choice",
			"choice":        choice,
			"probabilities": probabilities,
			"confidence":    0.55,
			"action":        map[string]any{"act_probability": 1.0},
		},
	})
}

func noulReply(noul float64) string {
	return wireReply(map[string]any{"item": map[string]any{"type": "noul", "noul": noul}})
}

func scoreReply(score float64, probabilities map[string]float64) string {
	return wireReply(map[string]any{
		"item": map[string]any{"type": "score", "score": score, "probabilities": probabilities},
	})
}

func decideServer(t *testing.T, p *probe, reply func(state string) (string, bool)) *httptest.Server {
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			t.Errorf("the endpoint is /v1/systemone, got %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req struct {
			State     string                     `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		p.mu.Lock()
		p.states = append(p.states, req.State)
		p.questions = append(p.questions, req.Questions)
		p.requests++
		p.inFlight++
		if p.inFlight > p.maxInFlight {
			p.maxInFlight = p.inFlight
		}
		p.mu.Unlock()
		if p.delay > 0 {
			time.Sleep(p.delay)
		}
		p.mu.Lock()
		p.inFlight--
		p.mu.Unlock()
		body, ok := reply(req.State)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	return srv
}

func httpDecider(t *testing.T, url string) decision.Decider {
	t.Helper()
	dec, err := decision.NewHTTP(decision.HTTPOptions{URL: url, Client: &http.Client{Transport: testenv.Transport()}})
	if err != nil {
		t.Fatal(err)
	}
	return dec
}

func testDecide(t *testing.T, dec decision.Decider, rec decision.Recorder, parallel int) *decision.Decide {
	t.Helper()
	d, err := decision.NewDecide(decision.DecideOptions{Decider: dec, Recorder: rec, Parallel: parallel})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func choiceArgs(items ...string) json.RawMessage {
	return jsonArgs(map[string]any{
		"kind":   "choice",
		"prompt": "What risk does this call carry?",
		"labels": []any{
			map[string]any{"label": "safe", "description": "reads or lists; nothing on disk changes"},
			map[string]any{"label": "changes", "description": "writes only inside the workspace it named"},
			map[string]any{"label": "dangerous", "description": "reaches outside the workspace, deletes, or can destroy state"},
		},
		"items": items,
	})
}

func jsonArgs(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func byState(replies map[string]string) func(string) (string, bool) {
	return func(state string) (string, bool) {
		r, ok := replies[state]
		return r, ok
	}
}

func openStore(t *testing.T) store.DB {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "decision.sqlite"), decisionstore.Statements(), decisionstore.SchemaVersion, decisionstore.Migration())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestThreeLabelsGroupCorrectly(t *testing.T) {
	p := &probe{}
	items := []string{
		"read the file",
		"write the report",
		"list the directory",
		"delete the backup",
		"touch the lockfile",
	}
	replies := map[string]string{
		"read the file":      choiceReply("safe", map[string]float64{"safe": 0.71, "changes": 0.2, "dangerous": 0.09}),
		"list the directory": choiceReply("safe", map[string]float64{"safe": 0.8, "changes": 0.1, "dangerous": 0.1}),
		"write the report":   choiceReply("changes", map[string]float64{"safe": 0.1, "changes": 0.7, "dangerous": 0.2}),
		"touch the lockfile": choiceReply("changes", map[string]float64{"safe": 0.2, "changes": 0.6, "dangerous": 0.2}),
		"delete the backup":  choiceReply("dangerous", map[string]float64{"safe": 0.05, "changes": 0.15, "dangerous": 0.8}),
	}
	srv := decideServer(t, p, byState(replies))
	d := testDecide(t, httpDecider(t, srv.URL), nil, 2)
	out, err := d.Exec(context.Background(), choiceArgs(items...))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	want := []string{
		"safe (2): #1 read the file; #3 list the directory",
		"changes (2): #2 write the report; #5 touch the lockfile",
		"dangerous (1): #4 delete the backup",
	}
	if len(lines) != len(want) {
		t.Fatalf("the reply is the three groups and nothing else:\n%q", out)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("line %d = %q, want %q (full reply %q)", i, lines[i], w, out)
		}
	}
	if requests, states, _ := p.snapshot(); requests != 5 || len(states) != 5 {
		t.Fatalf("one request per item: %d requests, states %v", requests, states)
	}
}

func TestAnItemUnderHalfComesBackInFullUnderUnsure(t *testing.T) {
	p := &probe{}
	wobbly := "maybe move the config\nthe second line is here"
	replies := map[string]string{
		"read the file":      choiceReply("safe", map[string]float64{"safe": 0.7, "changes": 0.2, "dangerous": 0.1}),
		wobbly:               choiceReply("changes", map[string]float64{"safe": 0.4, "changes": 0.3, "dangerous": 0.3}),
		"list the directory": choiceReply("safe", map[string]float64{"safe": 0.8, "changes": 0.1, "dangerous": 0.1}),
	}
	srv := decideServer(t, p, byState(replies))
	d := testDecide(t, httpDecider(t, srv.URL), nil, 2)
	out, err := d.Exec(context.Background(), choiceArgs("read the file", wobbly, "list the directory"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "safe (2): #1 read the file; #3 list the directory") {
		t.Fatalf("the sure items group:\n%q", out)
	}
	if !strings.Contains(out, "unsure (1)") {
		t.Fatalf("the unsure section is missing:\n%q", out)
	}
	if !strings.Contains(out, wobbly) {
		t.Fatalf("the unsure item comes back in full:\n%q", out)
	}
	if strings.Contains(out, "changes (") {
		t.Fatalf("an unsure item must not sit in a label group:\n%q", out)
	}
}

func TestExactlyHalfIsSure(t *testing.T) {
	p := &probe{}
	replies := map[string]string{
		"read the file": choiceReply("safe", map[string]float64{"safe": 0.5, "changes": 0.5}),
	}
	srv := decideServer(t, p, byState(replies))
	d := testDecide(t, httpDecider(t, srv.URL), nil, 1)
	out, err := d.Exec(context.Background(), choiceArgs("read the file"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "unsure") {
		t.Fatalf("a top probability of exactly one half is sure:\n%q", out)
	}
	if !strings.Contains(out, "safe (1): #1 read the file") {
		t.Fatalf("the item groups under its label:\n%q", out)
	}
}

func TestAnOverSizeListRefusesBeforeAnyRequest(t *testing.T) {
	p := &probe{}
	srv := decideServer(t, p, byState(nil))
	d := testDecide(t, httpDecider(t, srv.URL), nil, 2)
	items := []string{strings.Repeat("x", filetool.ReadCap-1), "x"}
	_, err := d.Exec(context.Background(), choiceArgs(items...))
	if err == nil {
		t.Fatal("a list at the read ceiling refuses")
	}
	if !strings.Contains(err.Error(), "read ceiling") {
		t.Fatalf("the refusal names the bound: %v", err)
	}
	if requests, _, _ := p.snapshot(); requests != 0 {
		t.Fatalf("the bound holds ahead of any I/O: %d requests went out", requests)
	}
}

func TestEachItemWritesOneStoreRow(t *testing.T) {
	p := &probe{}
	wobbly := "maybe move the config\nthe second line is here"
	replies := map[string]string{
		"read the file":      choiceReply("safe", map[string]float64{"safe": 0.7, "changes": 0.2, "dangerous": 0.1}),
		wobbly:               choiceReply("changes", map[string]float64{"safe": 0.4, "changes": 0.3, "dangerous": 0.3}),
		"list the directory": choiceReply("safe", map[string]float64{"safe": 0.8, "changes": 0.1, "dangerous": 0.1}),
	}
	srv := decideServer(t, p, byState(replies))
	db := openStore(t)
	rec := decisionstore.Recorder{DB: db, Scope: "proj"}
	d := testDecide(t, httpDecider(t, srv.URL), rec, 2)
	ctx := core.WithSession(context.Background(), &core.Session{ID: "sess-9"})
	items := []string{"read the file", wobbly, "list the directory"}
	if _, err := d.Exec(ctx, choiceArgs(items...)); err != nil {
		t.Fatal(err)
	}
	rows := map[int]row{}
	if err := scanRows(db, rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("one row per item, got %d", len(rows))
	}
	wantAnswer := map[int]string{1: "safe", 2: "changes", 3: "safe"}
	wantConf := map[int]float64{1: 0.7, 2: 0.3, 3: 0.8}
	wantUnsure := map[int]bool{1: false, 2: true, 3: false}
	for i := 1; i <= 3; i++ {
		r := rows[i]
		if r.site != decision.SiteDecide || r.status != decision.StatusFinal {
			t.Fatalf("row %d: site %q status %q, want a final decide row", i, r.site, r.status)
		}
		if r.answer != wantAnswer[i] {
			t.Fatalf("row %d: answer %q, want %q", i, r.answer, wantAnswer[i])
		}
		if r.confidence != wantConf[i] {
			t.Fatalf("row %d: confidence %g, want %g", i, r.confidence, wantConf[i])
		}
		if r.unsure != wantUnsure[i] {
			t.Fatalf("row %d: unsure %v, want %v", i, r.unsure, wantUnsure[i])
		}
		if r.session != "sess-9" {
			t.Fatalf("row %d: session %q, want sess-9", i, r.session)
		}
		if r.scope != "proj" {
			t.Fatalf("row %d: scope %q, want proj", i, r.scope)
		}
		if r.decider != host(srv) {
			t.Fatalf("row %d: decider %q, want the server host %q", i, r.decider, host(srv))
		}
		wantState, err := json.Marshal(map[string]string{"item": items[i-1]})
		if err != nil {
			t.Fatal(err)
		}
		if r.state != string(wantState) {
			t.Fatalf("row %d: state %q, want %q", i, r.state, wantState)
		}
	}
}

type row struct {
	site       string
	status     string
	answer     string
	confidence float64
	unsure     bool
	session    string
	scope      string
	decider    string
	state      string
}

func scanRows(db store.DB, rows map[int]row) error {
	res, err := db.Query(`SELECT id, site, status, answer, confidence, unsure, session, scope, decider, state FROM decisions ORDER BY id`)
	if err != nil {
		return err
	}
	defer res.Close()
	for res.Next() {
		var (
			r     row
			id    int
			proxy any
		)
		if err := res.Scan(&id, &r.site, &r.status, &r.answer, &r.confidence, &proxy, &r.session, &r.scope, &r.decider, &r.state); err != nil {
			return err
		}
		switch v := proxy.(type) {
		case bool:
			r.unsure = v
		case int64:
			r.unsure = v != 0
		}
		rows[id] = r
	}
	return res.Err()
}

func host(srv *httptest.Server) string {
	u, err := url.Parse(srv.URL)
	if err != nil {
		panic(err)
	}
	return u.Host
}

func TestAServerErrorRefusesAndWritesNoRows(t *testing.T) {
	p := &probe{}
	replies := map[string]string{
		"one": choiceReply("safe", map[string]float64{"safe": 0.9, "changes": 0.1}),
	}
	srv := decideServer(t, p, byState(replies))
	db := openStore(t)
	d := testDecide(t, httpDecider(t, srv.URL), decisionstore.Recorder{DB: db, Scope: "proj"}, 1)
	_, err := d.Exec(context.Background(), choiceArgs("one", "two"))
	if err == nil {
		t.Fatal("a server error is a refusal")
	}
	if !strings.Contains(err.Error(), "decide") {
		t.Fatalf("the refusal names the tool: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM decisions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("a refused call writes no rows, got %d", count)
	}
}

func TestAMissingAnswerRefuses(t *testing.T) {
	p := &probe{}
	srv := decideServer(t, p, func(string) (string, bool) { return wireReply(map[string]any{}), true })
	d := testDecide(t, httpDecider(t, srv.URL), nil, 1)
	if _, err := d.Exec(context.Background(), choiceArgs("one")); err == nil {
		t.Fatal("an item with no answer refuses")
	}
}

func TestArgsRefuseBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{"an unknown kind", `{"kind":"vibe","prompt":"p","items":["a"]}`},
		{"no prompt", `{"kind":"binary","items":["a"]}`},
		{"one label", `{"kind":"choice","prompt":"p","labels":[{"label":"a"}],"items":["a"]}`},
		{"duplicate labels", `{"kind":"choice","prompt":"p","labels":[{"label":"a"},{"label":"a"}],"items":["a"]}`},
		{"no items", `{"kind":"binary","prompt":"p","items":[]}`},
		{"an empty item", `{"kind":"binary","prompt":"p","items":[""]}`},
		{"score without criteria", `{"kind":"score","prompt":"p","items":["a"]}`},
		{"an unknown field", `{"kind":"binary","prompt":"p","items":["a"],"mood":"up"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &probe{}
			srv := decideServer(t, p, byState(nil))
			d := testDecide(t, httpDecider(t, srv.URL), nil, 1)
			if _, err := d.Exec(context.Background(), json.RawMessage(c.args)); err == nil {
				t.Fatal("the args refuse")
			}
			if requests, _, _ := p.snapshot(); requests != 0 {
				t.Fatalf("the refusal holds ahead of any I/O: %d requests", requests)
			}
		})
	}
}

func TestOneRequestPerItemSpeaksTheWire(t *testing.T) {
	p := &probe{}
	items := []string{"alpha", "beta", "gamma", "delta"}
	srv := decideServer(t, p, func(string) (string, bool) {
		return choiceReply("safe", map[string]float64{"safe": 0.9, "changes": 0.1}), true
	})
	d := testDecide(t, httpDecider(t, srv.URL), nil, 4)
	if _, err := d.Exec(context.Background(), choiceArgs(items...)); err != nil {
		t.Fatal(err)
	}
	requests, states, questions := p.snapshot()
	if requests != 4 || len(states) != 4 {
		t.Fatalf("one request per item: %d requests", requests)
	}
	seen := make(map[string]bool, len(items))
	for _, state := range states {
		seen[state] = true
	}
	for _, item := range items {
		if !seen[item] {
			t.Fatalf("the item %q went out as a state, got %v", item, states)
		}
	}
	for i, qs := range questions {
		if len(qs) != 1 {
			t.Fatalf("request %d carries one question, got %d", i, len(qs))
		}
		raw, ok := qs["item"]
		if !ok {
			t.Fatalf("request %d: the question is keyed by the item id: %v", i, qs)
		}
		var q struct {
			Type         string            `json:"type"`
			Instructions string            `json:"instructions"`
			Criteria     map[string]string `json:"criteria"`
		}
		if err := json.Unmarshal(raw, &q); err != nil {
			t.Fatal(err)
		}
		if q.Type != "choice" || q.Instructions != "What risk does this call carry?" {
			t.Fatalf("request %d: the question rides the wire shape: %+v", i, q)
		}
		if len(q.Criteria) != 3 || q.Criteria["safe"] == "" || q.Criteria["changes"] == "" || q.Criteria["dangerous"] == "" {
			t.Fatalf("request %d: the labels ride as criteria: %+v", i, q.Criteria)
		}
	}
}

func TestParallelOneSerializes(t *testing.T) {
	p := &probe{delay: 30 * time.Millisecond}
	srv := decideServer(t, p, func(string) (string, bool) {
		return choiceReply("safe", map[string]float64{"safe": 0.9, "changes": 0.1}), true
	})
	d := testDecide(t, httpDecider(t, srv.URL), nil, 1)
	if _, err := d.Exec(context.Background(), choiceArgs("one", "two", "three")); err != nil {
		t.Fatal(err)
	}
	if m := p.maxConcurrent(); m != 1 {
		t.Fatalf("parallel one bounds the requests to one at a time, got %d in flight", m)
	}
}

func TestYesNoGroupsAndIsNeverUnsure(t *testing.T) {
	p := &probe{}
	replies := map[string]string{
		"ls":     noulReply(0.7),
		"rm -rf": noulReply(0.2),
	}
	srv := decideServer(t, p, byState(replies))
	d := testDecide(t, httpDecider(t, srv.URL), nil, 2)
	out, err := d.Exec(context.Background(), jsonArgs(map[string]any{
		"kind":   "binary",
		"prompt": "Is this a read?",
		"items":  []string{"ls", "rm -rf"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	want := []string{"yes (1): #1 ls", "no (1): #2 rm -rf"}
	if len(lines) != 2 || lines[0] != want[0] || lines[1] != want[1] {
		t.Fatalf("the yes/no groups:\n%q", out)
	}
	if strings.Contains(out, "unsure") {
		t.Fatalf("a yes/no is never unsure:\n%q", out)
	}
}

func TestScoreGroupsByItsValue(t *testing.T) {
	p := &probe{}
	replies := map[string]string{
		"one": scoreReply(2, map[string]float64{"2": 0.8, "1": 0.2}),
		"two": scoreReply(1, map[string]float64{"1": 0.9, "0": 0.1}),
	}
	srv := decideServer(t, p, byState(replies))
	d := testDecide(t, httpDecider(t, srv.URL), nil, 2)
	out, err := d.Exec(context.Background(), jsonArgs(map[string]any{
		"kind":     "score",
		"prompt":   "How hot is this?",
		"criteria": []string{"cold", "warm", "hot"},
		"items":    []string{"one", "two"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	want := []string{"2 (1): #1 one", "1 (1): #2 two"}
	if len(lines) != 2 || lines[0] != want[0] || lines[1] != want[1] {
		t.Fatalf("the score groups:\n%q", out)
	}
}

func TestANilRecorderRecordsNothing(t *testing.T) {
	p := &probe{}
	srv := decideServer(t, p, func(string) (string, bool) {
		return choiceReply("safe", map[string]float64{"safe": 0.9, "changes": 0.1}), true
	})
	d := testDecide(t, httpDecider(t, srv.URL), nil, 1)
	out, err := d.Exec(context.Background(), choiceArgs("one"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "safe (1): #1 one") {
		t.Fatalf("the reply stands without a recorder:\n%q", out)
	}
}

func TestTheTriggerLivesInTheToolDescription(t *testing.T) {
	d := tool.Def("decide").Description()
	if !strings.Contains(d, "instead of finding it out yourself") {
		t.Fatalf("decide's description carries the trigger: %q", d)
	}
	if strings.Contains(d, "hand the items to decide") {
		t.Fatalf("the old system-prompt wording is gone: %q", d)
	}
}
