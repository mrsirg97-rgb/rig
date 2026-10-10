package rem

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/v2/store"
	"github.com/mrsirg97-rgb/rig/v2/store/fts"
)

var noiseTokens = []string{
	"qzxvkjwb", "wbnfqzxv", "kjwbqzxv", "vkwbnfqz", "bnfqzxvk",
	"zxvkjwbn", "jwbnfqzx", "fqzxvkwb", "vkjwbnfq", "wbnqzxvk",
}

func TestFtsArmOrsTokensThroughQueryLength(t *testing.T) {
	db := newDB(t)
	content := "the scheduler folds drift by rereading the crontab every hour"
	learn(t, db, "/ws1", content, nil)
	tokens := fts.Tokenize(content)
	for n := 1; n <= 20; n++ {
		parts := make([]string, 0, n)
		for i := 0; i < n; i++ {
			if i%2 == 0 {
				parts = append(parts, tokens[(i/2)%len(tokens)])
			} else {
				parts = append(parts, noiseTokens[(i/2)%len(noiseTokens)])
			}
		}
		query := strings.Join(parts, " ")
		_, hits, err := Recall(context.Background(), db, "/ws1", RecallInput{Query: query, K: 10})
		if err != nil {
			t.Fatalf("n=%d query %q: %v", n, query, err)
		}
		hit := findContent(t, hits, "crontab")
		if hit == nil {
			t.Fatalf("n=%d query %q: the fts arm must carry the memory through any length, got %+v", n, query, hits)
		}
		if hit.Match == "fuzzy" {
			t.Fatalf("n=%d query %q: match %q, the fts arm must not hand the query to the fuzzy arm", n, query, hit.Match)
		}
	}
}

func TestRecallLineNamesArmAfterStrength(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "the api returns 429 when the token expires", nil)
	reply, hits, err := Recall(context.Background(), db, "/ws1", RecallInput{Query: "token expires", K: 10})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits %+v", hits)
	}
	want := fmt.Sprintf("m%d [%.2f %s]", hits[0].ID, hits[0].EffectiveStrength, hits[0].Match)
	if !strings.Contains(reply, want) {
		t.Fatalf("reply %q must name the arm after the strength: %q", reply, want)
	}
}

func TestBrowseLineNamesBrowseAfterStrength(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "the api returns 429 when the token expires", nil)
	reply, _, err := Recall(context.Background(), db, "/ws1", RecallInput{K: 10})
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if !strings.Contains(reply, " browse]") {
		t.Fatalf("browse reply %q must name its arm", reply)
	}
}

func TestRecallRefusesUnreadableLastConsolidated(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "the api returns 429 when the token expires", nil)
	corruptLastConsolidated(t, db, "the third of february, two thousand")
	_, _, err := Recall(context.Background(), db, "/ws1", RecallInput{Query: "token expires", K: 10})
	refuseMustName(t, err)
	row := memByID(t, db, 1)
	if row.AccessCount != 0 || row.LastAccessedAt != nil {
		t.Fatalf("a refused recall must not reinforce: access %d, last access %v", row.AccessCount, row.LastAccessedAt)
	}
}

func TestRecallEmptyLastConsolidatedIsNever(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "the api returns 429 when the token expires", nil)
	corruptLastConsolidated(t, db, "")
	_, hits, err := Recall(context.Background(), db, "/ws1", RecallInput{Query: "token expires", K: 10})
	if err != nil {
		t.Fatalf("an empty timestamp is never, not corruption: %v", err)
	}
	if !hasContent(t, hits, "the api returns 429 when the token expires") {
		t.Fatalf("hits %+v", hits)
	}
}

func TestPruneConsolidateRefusesUnreadableLastConsolidated(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "the api returns 429 when the token expires", nil)
	corruptLastConsolidated(t, db, "the third of february, two thousand")
	_, _, err := Prune(context.Background(), db, "/ws1", PruneInput{Verb: "consolidate"})
	refuseMustName(t, err)
}

func TestPruneConsolidateRefusesFutureLastConsolidated(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "the api returns 429 when the token expires", nil)
	corruptLastConsolidated(t, db, time.Now().UTC().AddDate(0, 0, 1).Format(time.RFC3339))
	_, _, err := Prune(context.Background(), db, "/ws1", PruneInput{Verb: "consolidate"})
	refuseMustName(t, err)
}

func TestDaysSinceEmptyIsNever(t *testing.T) {
	days, err := daysSince("", time.Now().UTC())
	if err != nil {
		t.Fatalf("daysSince empty: %v", err)
	}
	if days != 0 {
		t.Fatalf("days %v, want 0", days)
	}
}

func TestDaysSinceRefusesUnparseableAndFuture(t *testing.T) {
	now := time.Now().UTC()
	if _, err := daysSince("the third of february, two thousand", now); err == nil {
		t.Fatal("an unparseable timestamp must refuse")
	}
	if _, err := daysSince(now.Add(time.Hour).Format(time.RFC3339), now); err == nil {
		t.Fatal("a future timestamp must refuse")
	}
}

func corruptLastConsolidated(t *testing.T, db store.DB, value string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE memories SET last_consolidated_at = ?`, value); err != nil {
		t.Fatal(err)
	}
}

func refuseMustName(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("a corrupt last_consolidated_at must refuse")
	}
	for _, want := range []string{"m1", "unreadable last_consolidated_at", "prune it by id"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must name %q", err, want)
		}
	}
}
