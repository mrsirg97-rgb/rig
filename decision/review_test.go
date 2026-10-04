package decision_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2"
	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/decision"
	"github.com/mrsirg97-rgb/rig/v2/evt"
	"github.com/mrsirg97-rgb/rig/v2/models"
	"github.com/mrsirg97-rgb/rig/v2/store"
	decisionstore "github.com/mrsirg97-rgb/rig/v2/store/decision"
)

type storeReviews struct {
	db store.DB
}

func (r storeReviews) Pending(ctx context.Context) ([]decision.ReviewRow, error) {
	rows, err := decisionstore.Pending(ctx, r.db)
	if err != nil {
		return nil, err
	}
	out := make([]decision.ReviewRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, decision.ReviewRow{
			ID: row.ID, Site: row.Site, State: row.State, Question: row.Question,
			Answer: row.Answer, Confidence: row.Confidence, Decider: row.Decider,
		})
	}
	return out, nil
}

func (r storeReviews) Settle(ctx context.Context, id int64, approved bool, reviewer, answer string) error {
	return decisionstore.Settle(ctx, r.db, decisionstore.SettleInput{
		ID: id, Approved: approved, Reviewer: reviewer, ReviewerAnswer: answer,
	})
}

func (r storeReviews) Settled(ctx context.Context, site string, q decision.Question, state string) (string, bool, error) {
	return decisionstore.Settled(ctx, r.db, site, q, state)
}

type fakeFire struct {
	stdouts []string
	calls   int
	prompts []string
	fired   chan struct{}
}

func (f *fakeFire) fire(ctx context.Context, prompt string, voice broadcast.Member) (string, error) {
	f.calls++
	f.prompts = append(f.prompts, prompt)
	if f.fired != nil {
		f.fired <- struct{}{}
	}
	if f.calls <= len(f.stdouts) {
		speak(voice, f.stdouts[f.calls-1])
	}
	return "dsv4", nil
}

type verdictFire struct {
	mu      sync.Mutex
	prompts []string
	fired   chan struct{}
	reply   string
}

func (f *verdictFire) fire(ctx context.Context, prompt string, voice broadcast.Member) (string, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, prompt)
	f.mu.Unlock()
	if f.fired != nil {
		f.fired <- struct{}{}
	}
	if f.reply != "" {
		speak(voice, f.reply)
		return "dsv4", nil
	}
	var b strings.Builder
	for _, id := range promptIds(prompt) {
		fmt.Fprintf(&b, "verdict: %s approve\n", id)
	}
	speak(voice, b.String())
	return "dsv4", nil
}

func (f *verdictFire) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prompts)
}

func (f *verdictFire) prompt(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prompts[i]
}

func promptIds(prompt string) []string {
	var ids []string
	for i, seg := range strings.Split(prompt, "\n== ") {
		if i == 0 {
			continue
		}
		if id, _, ok := strings.Cut(seg, " "); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func verdicts(from, to int) string {
	var b strings.Builder
	for id := from; id <= to; id++ {
		fmt.Fprintf(&b, "verdict: %d approve\n", id)
	}
	return b.String()
}

func openReviewedStore(t *testing.T, n int) store.DB {
	t.Helper()
	db, _, _, err := store.Open(filepath.Join(t.TempDir(), "decision.sqlite"), decisionstore.Statements(), decisionstore.SchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if _, err := decisionstore.Propose(ctx, db, decisionstore.ProposeInput{
			Scope: "proj", Site: decision.SiteBash, State: `{"command":"ls"}`,
			Question:   decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
			Answer:     "safe",
			Confidence: 0.71,
			Decider:    "laya",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func reviewer(db store.DB, f *fakeFire) *decision.Reviewer {
	return reviewerBatch(db, f.fire, 1<<30)
}

func reviewerBatch(db store.DB, fire decision.Fire, batch int) *decision.Reviewer {
	return reviewerRow(db, fire, batch, models.Model{Window: 1 << 30, Reserve: 0, MaxTokens: 1 << 30})
}

func reviewerRow(db store.DB, fire decision.Fire, batch int, row models.Model) *decision.Reviewer {
	engine := evt.NewEngine()
	go engine.Start(context.Background())
	room := broadcast.NewRoom("test", func(id int64) broadcast.Transport {
		return broadcast.NewLoopTransport(id, engine, rig.PriorityFleet)
	})
	return decision.NewReviewer(context.Background(), engine, storeReviews{db: db}, fire, batch, row, room)
}

func waitFires(t *testing.T, fired <-chan struct{}, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-fired:
		case <-time.After(5 * time.Second):
			t.Fatalf("the bite fired %d of %d", i, n)
		}
	}
}

func waitSettled(t *testing.T, db store.DB, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM decisions WHERE status = 'pending'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the rows never settled to %d pending", want)
}

func TestThreePendingRowsAreReviewedInOneFire(t *testing.T) {
	db := openReviewedStore(t, 3)
	f := &fakeFire{stdouts: []string{"reading\nverdict: 1 approve\nverdict: 2 approve\nverdict: 3 approve"}}
	r := reviewer(db, f)
	if _, err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Fatalf("every pending row is one fire, got %d fires", f.calls)
	}
	if !strings.Contains(f.prompts[0], "call the verdict tool") || !strings.Contains(f.prompts[0], "What risk does this bash call carry?") {
		t.Fatalf("the prompt carries the rows and the contract: %q", f.prompts[0])
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 0 {
		t.Fatalf("all three settled: %d rows pending", len(rows))
	}
	var approved int
	if err := db.QueryRow(`SELECT count(*) FROM decisions WHERE status = 'approved'`).Scan(&approved); err != nil {
		t.Fatal(err)
	}
	if approved != 3 {
		t.Fatalf("all three approved, got %d", approved)
	}
}

func TestADenyStoresTheCorrectedAnswer(t *testing.T) {
	db := openReviewedStore(t, 1)
	f := &fakeFire{stdouts: []string{"the call removes a tree\nverdict: 1 deny changes"}}
	r := reviewer(db, f)
	if _, err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	var status, rev string
	var correction *string
	if err := db.QueryRow(`SELECT status, reviewer, reviewer_answer FROM decisions WHERE id = 1`).Scan(&status, &rev, &correction); err != nil {
		t.Fatal(err)
	}
	if status != decision.StatusDenied || rev != "dsv4" || correction == nil || *correction != "changes" {
		t.Fatalf("the deny stores the correction and the reviewer: %q %q %v", status, rev, correction)
	}
}

func TestAPartialReplyLeavesTheUnnamedRowPending(t *testing.T) {
	db := openReviewedStore(t, 2)
	f := &fakeFire{stdouts: []string{"verdict: 1 approve"}}
	r := reviewer(db, f)
	if _, err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 1 || rows[0].ID != 2 {
		t.Fatalf("the unnamed row stays pending: %+v", rows)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM decisions WHERE id = 1`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != decision.StatusApproved {
		t.Fatalf("the named row settled: %q", status)
	}
}

func TestADenyWithoutACorrectionIsNotAVerdict(t *testing.T) {
	db := openReviewedStore(t, 1)
	f := &fakeFire{stdouts: []string{"verdict: 1 deny"}}
	r := reviewer(db, f)
	if _, err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 1 {
		t.Fatalf("the deny without a correction leaves the row pending: %d", len(rows))
	}
}

func TestABiteTakesTheOldestRowsAndLeavesTheRest(t *testing.T) {
	db := openReviewedStore(t, 12)
	f := &fakeFire{stdouts: []string{verdicts(1, 10), verdicts(11, 12)}}
	r := reviewerBatch(db, f.fire, 10)
	summary, err := r.Drain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 || strings.Count(f.prompts[0], "\n== ") != 10 {
		t.Fatalf("one bite is %d rows in %d fires", 10, f.calls)
	}
	if !strings.Contains(f.prompts[0], "\n== 1 ") || strings.Contains(f.prompts[0], "\n== 12 ") {
		t.Fatalf("the bite takes the oldest rows first: %.120s", f.prompts[0])
	}
	if !strings.Contains(summary, "fired 10 rows, settled 10, 2 stay pending") {
		t.Fatalf("the summary names the bite and the backlog: %q", summary)
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 2 || rows[0].ID != 11 {
		t.Fatalf("the rest stay pending oldest first: %+v", rows)
	}
	if summary, err = r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "fired 2 rows, settled 2, 0 stay pending") {
		t.Fatalf("the last bite is the remainder: %q", summary)
	}
	if rows, _ = decisionstore.Pending(context.Background(), db); len(rows) != 0 {
		t.Fatalf("the backlog converged: %d pending", len(rows))
	}
}

func TestTwoHundredSixtyFourRowsBiteTenAtATimeAcrossTwentySevenWakes(t *testing.T) {
	db := openReviewedStore(t, 264)
	f := &verdictFire{fired: make(chan struct{}, 64)}
	r := reviewerBatch(db, f.fire, 10)

	for i := 0; i < 27; i++ {
		r.Land()
		r.Wake()
		remaining := 264 - 10*(i+1)
		if remaining < 0 {
			remaining = 0
		}
		waitSettled(t, db, remaining)
	}
	waitFires(t, f.fired, 27)
	if f.calls() != 27 {
		t.Fatalf("264 rows bite across 27 wakes, got %d fires", f.calls())
	}
	for i := 0; i < 27; i++ {
		want := 10
		if i == 26 {
			want = 4
		}
		if got := strings.Count(f.prompt(i), "\n== "); got != want {
			t.Fatalf("bite %d fired %d rows, want %d", i, got, want)
		}
		if !strings.Contains(f.prompt(i), fmt.Sprintf("\n== %d ", 10*i+1)) {
			t.Fatalf("bite %d is not the oldest rows: %.120s", i, f.prompt(i))
		}
	}
	if strings.Contains(f.prompt(0), "\n== 11 ") {
		t.Fatalf("the first bite stops at the batch: %.120s", f.prompt(0))
	}
	if !strings.Contains(f.prompt(26), "\n== 261 ") || !strings.Contains(f.prompt(26), "\n== 264 ") {
		t.Fatalf("the last bite is the remainder of four: %.120s", f.prompt(26))
	}
}

func TestAQuietTurnEndWithABacklogStillTakesABite(t *testing.T) {
	db := openReviewedStore(t, 12)
	f := &verdictFire{fired: make(chan struct{}, 64)}
	r := reviewerBatch(db, f.fire, 10)

	r.Land()
	r.Wake()
	waitSettled(t, db, 2)
	r.Wake()
	waitSettled(t, db, 0)
	waitFires(t, f.fired, 2)
	if f.calls() != 2 {
		t.Fatalf("the backlog left dirty by a bite takes the next turn end, got %d fires", f.calls())
	}
}

func TestReviewBatchZeroWakesAndFiresNothing(t *testing.T) {
	db := openReviewedStore(t, 3)
	f := &verdictFire{fired: make(chan struct{}, 64)}
	r := reviewerBatch(db, f.fire, 0)

	r.Land()
	r.Wake()
	if _, err := decisionstore.Propose(context.Background(), db, decisionstore.ProposeInput{
		Scope: "proj", Site: decision.SiteBash, State: `{"command":"ls"}`,
		Question:   decision.Choice("risk", "What risk does this bash call carry?", "safe", "changes", "dangerous"),
		Answer:     "safe",
		Confidence: 0.71,
		Decider:    "laya",
	}); err != nil {
		t.Fatal(err)
	}
	r.Land()
	r.Wake()
	time.Sleep(200 * time.Millisecond)
	if f.calls() != 0 {
		t.Fatalf("reviewBatch 0 fires nothing while the rows land, got %d fires", f.calls())
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 4 {
		t.Fatalf("the rows stay pending for a reviewer that is off: %d pending", len(rows))
	}
}

func TestANegativeBatchRefusesLoud(t *testing.T) {
	db := openReviewedStore(t, 1)
	f := &verdictFire{}
	r := reviewerBatch(db, f.fire, -1)
	_, err := r.Drain(context.Background())
	if err == nil || !strings.Contains(err.Error(), "reviewBatch") || !strings.Contains(err.Error(), "-1") {
		t.Fatalf("a negative batch refuses by name: %v", err)
	}
	if f.calls() != 0 {
		t.Fatalf("a refused batch fires nothing, got %d fires", f.calls())
	}
}

func TestAGarbageBiteWaitsForTheNextLanding(t *testing.T) {
	db := openReviewedStore(t, 12)
	f := &verdictFire{fired: make(chan struct{}, 64), reply: "I have no idea"}
	r := reviewerBatch(db, f.fire, 10)

	r.Land()
	r.Wake()
	<-f.fired
	time.Sleep(200 * time.Millisecond)
	r.Wake()
	time.Sleep(200 * time.Millisecond)
	if f.calls() != 1 {
		t.Fatalf("a fire that answers nothing waits for the next landing, got %d fires", f.calls())
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 12 {
		t.Fatalf("nothing settled: %d pending", len(rows))
	}
	r.Land()
	r.Wake()
	<-f.fired
	if f.calls() != 2 {
		t.Fatalf("a landing wakes the stalled reviewer, got %d fires", f.calls())
	}
}

func TestAFireThatSettlesNothingWithNoCutWaitsForTheNextLanding(t *testing.T) {
	db := openReviewedStore(t, 2)
	f := &fakeFire{stdouts: []string{"I have no idea"}, fired: make(chan struct{}, 4)}
	r := reviewer(db, f)

	r.Land()
	r.Wake()
	<-f.fired
	r.Wake()
	select {
	case <-f.fired:
		t.Fatal("a fire that settles nothing waits for the next landing, not the next turn end")
	case <-time.After(200 * time.Millisecond):
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 2 {
		t.Fatalf("nothing settled: %d pending", len(rows))
	}
}

func TestALandingMarksDirtyAndTheTurnEndWakes(t *testing.T) {
	db := openReviewedStore(t, 1)
	f := &fakeFire{stdouts: []string{"verdict: 1 approve"}, fired: make(chan struct{}, 4)}
	r := reviewer(db, f)

	r.Land()
	r.Wake()
	select {
	case <-f.fired:
	case <-time.After(2 * time.Second):
		t.Fatal("a landing marks the reviewer dirty; the turn end is the wake")
	}
}

func TestATurnEndWithNoLandingCostsNothing(t *testing.T) {
	db := openReviewedStore(t, 1)
	f := &fakeFire{stdouts: []string{"verdict: 1 approve"}, fired: make(chan struct{}, 4)}
	r := reviewer(db, f)

	r.Wake()
	select {
	case <-f.fired:
		t.Fatal("a turn end with no landing behind it reviews nothing")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTheRowsPastTheBudgetStayDirtyForTheNextTurnEnd(t *testing.T) {
	db := openReviewedStore(t, 2)
	_, err := db.Exec(`UPDATE decisions SET state = ?`, strings.Repeat("x", 8000))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeFire{stdouts: []string{"verdict: 1 approve", "verdict: 2 approve"}, fired: make(chan struct{}, 4)}
	r := reviewerRow(db, f.fire, 10, models.Model{Window: 1500, Reserve: 500, MaxTokens: 1 << 30})

	r.Land()
	r.Wake()
	waitSettled(t, db, 1)
	r.Wake()
	waitSettled(t, db, 0)
	waitFires(t, f.fired, 2)
	if f.calls != 2 {
		t.Fatalf("the overflow converged over the turn ends, got %d fires", f.calls)
	}
}

func TestAFireTakesTheOldestRowsThatFitTheWindow(t *testing.T) {
	db := openReviewedStore(t, 2)
	_, err := db.Exec(`UPDATE decisions SET state = ?`, strings.Repeat("x", 8000))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeFire{stdouts: []string{"verdict: 1 approve", "verdict: 2 approve"}}
	r := reviewerRow(db, f.fire, 10, models.Model{Window: 1500, Reserve: 500, MaxTokens: 1 << 30})
	if _, err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 || strings.Count(f.prompts[0], "\n== ") != 1 {
		t.Fatalf("the fire took the one row that fits: %d fires, %d rows in the prompt", f.calls, strings.Count(f.prompts[0], "\n== "))
	}
	rows, _ := decisionstore.Pending(context.Background(), db)
	if len(rows) != 1 || rows[0].ID != 2 {
		t.Fatalf("the row past the budget stays pending: %+v", rows)
	}
}

func TestABatchAboveTheReplyCeilingIsCutToIt(t *testing.T) {
	db := openReviewedStore(t, 264)
	f := &fakeFire{stdouts: []string{verdicts(1, 40)}}
	r := reviewerRow(db, f.fire, 100, models.Model{Window: 1 << 30, Reserve: 0, MaxTokens: 40 * decision.VerdictCost()})
	summary, err := r.Drain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Fatalf("one wake is one fire, got %d fires", f.calls)
	}
	if rows := strings.Count(f.prompts[0], "\n== "); rows != 40 {
		t.Fatalf("a batch of 100 is cut to the 40 verdict lines the reply budget fits, got %d rows", rows)
	}
	if !strings.Contains(f.prompts[0], "\n== 1 ") || strings.Contains(f.prompts[0], "\n== 41 ") {
		t.Fatalf("the cut keeps the oldest rows: %.120s", f.prompts[0])
	}
	if !strings.Contains(summary, "fired 40 rows, settled 40, 224 stay pending") {
		t.Fatalf("the summary names the fire and what stays: %q", summary)
	}
	pending, _ := decisionstore.Pending(context.Background(), db)
	if len(pending) != 224 || pending[0].ID != 41 {
		t.Fatalf("the rest stay pending for the next turn end: %d", len(pending))
	}
}

func TestTheWindowBoundHoldsUnderAReplyBudgetThatFitsEverything(t *testing.T) {
	db := openReviewedStore(t, 2)
	_, err := db.Exec(`UPDATE decisions SET state = ?`, strings.Repeat("x", 8000))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeFire{stdouts: []string{"verdict: 1 approve", "verdict: 2 approve"}}
	r := reviewerRow(db, f.fire, 100, models.Model{Window: 1500, Reserve: 500, MaxTokens: 1 << 30})
	if _, err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rows := strings.Count(f.prompts[0], "\n== "); rows != 1 {
		t.Fatalf("the window minus the reserve is still the second bound: %d rows", rows)
	}
}

func TestAMaxOutputUnderOneVerdictLineStillFiresOneRow(t *testing.T) {
	db := openReviewedStore(t, 3)
	f := &fakeFire{stdouts: []string{"verdict: 1 approve\nverdict: 2 approve\nverdict: 3 approve"}}
	r := reviewerRow(db, f.fire, 10, models.Model{Window: 1 << 30, Reserve: 0, MaxTokens: decision.VerdictCost() - 1})
	if _, err := r.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rows := strings.Count(f.prompts[0], "\n== "); rows != 1 {
		t.Fatalf("one row still goes out under a budget that fits no line: %d rows", rows)
	}
}

func TestTheVerdictCostIsDerivedFromTheToolCall(t *testing.T) {
	cost := decision.VerdictCost()
	if cost <= 0 {
		t.Fatal("a verdict call costs tokens; the reply bound cannot be derived from zero")
	}
	if again := decision.VerdictCost(); again != cost {
		t.Fatalf("the derivation is not deterministic: %d then %d", cost, again)
	}
}

func TestADrainWithNothingPendingCostsNoFire(t *testing.T) {
	db := openReviewedStore(t, 0)
	f := &fakeFire{}
	r := reviewer(db, f)
	summary, err := r.Drain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Fatalf("nothing pending fires nothing, got %d fires", f.calls)
	}
	if summary == "" {
		t.Fatal("the drain still gets a summary")
	}
}

func voice(t *testing.T) (broadcast.Member, <-chan string) {
	t.Helper()
	engine := evt.NewEngine()
	go engine.Start(context.Background())
	t.Cleanup(engine.Stop)
	room := broadcast.NewRoom("test", func(id int64) broadcast.Transport {
		return broadcast.NewLoopTransport(id, engine, 0)
	})
	said := make(chan string, 8)
	room.Add(-1).Subscribe(context.Background(), func(err error, messages ...broadcast.Message) {
		for _, m := range messages {
			if n, ok := m.Event().(core.Notice); err == nil && ok {
				said <- n.Source + ": " + n.Text
			}
		}
	})
	return room.Add(0), said
}

func speak(voice broadcast.Member, script string) {
	for _, line := range strings.Split(script, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "verdict:")
		if !ok {
			continue
		}
		fields := strings.SplitN(strings.TrimSpace(rest), " ", 3)
		if len(fields) < 2 {
			continue
		}
		id, _ := strconv.ParseInt(fields[0], 10, 64)
		v := core.Verdict{Row: id, Accept: fields[1] == "approve"}
		if len(fields) == 3 {
			v.Reason = fields[2]
		}
		voice.Publish(context.Background(), func(error) {}, broadcast.NewMessage(voice.Id(), true, v))
	}
}
