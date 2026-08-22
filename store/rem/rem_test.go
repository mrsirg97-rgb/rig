package rem

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/store"
	remdom "github.com/mrsirg97-rgb/rig/store/rem/domain"
)

type probe struct {
	ID                 int64
	Scope              string
	ScopeLabel         string
	Kind               string
	Content            string
	Source             *string
	Importance         float64
	Strength           float64
	AccessCount        int64
	SupersededBy       *int64
	CreatedAt          string
	LastAccessedAt     *string
	LastConsolidatedAt string
	ContentMd5         string
}

func newDB(t *testing.T) store.DB {
	t.Helper()
	db, _, err := store.Open(filepath.Join(t.TempDir(), "rem.sqlite"), Statements(), SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return db
}

func memRow(t *testing.T, db store.DB, content string) *probe {
	t.Helper()
	row := db.QueryRow(`SELECT id, scope, scope_label, kind, content, source, importance,
		strength, access_count, superseded_by, created_at, last_accessed_at,
		last_consolidated_at, content_md5 FROM memories WHERE content = ?`, content)
	var p probe
	if err := row.Scan(&p.ID, &p.Scope, &p.ScopeLabel, &p.Kind, &p.Content, &p.Source,
		&p.Importance, &p.Strength, &p.AccessCount, &p.SupersededBy, &p.CreatedAt,
		&p.LastAccessedAt, &p.LastConsolidatedAt, &p.ContentMd5); err != nil {
		return nil
	}
	return &p
}

func memByID(t *testing.T, db store.DB, id int64) *probe {
	t.Helper()
	row := db.QueryRow(`SELECT id, scope, scope_label, kind, content, source, importance,
		strength, access_count, superseded_by, created_at, last_accessed_at,
		last_consolidated_at, content_md5 FROM memories WHERE id = ?`, id)
	var p probe
	if err := row.Scan(&p.ID, &p.Scope, &p.ScopeLabel, &p.Kind, &p.Content, &p.Source,
		&p.Importance, &p.Strength, &p.AccessCount, &p.SupersededBy, &p.CreatedAt,
		&p.LastAccessedAt, &p.LastConsolidatedAt, &p.ContentMd5); err != nil {
		return nil
	}
	return &p
}

func memRows(t *testing.T, db store.DB) []probe {
	t.Helper()
	rows, err := db.Query(`SELECT id, scope, scope_label, kind, content, source, importance,
		strength, access_count, superseded_by, created_at, last_accessed_at,
		last_consolidated_at, content_md5 FROM memories ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []probe
	for rows.Next() {
		var p probe
		if err := rows.Scan(&p.ID, &p.Scope, &p.ScopeLabel, &p.Kind, &p.Content, &p.Source,
			&p.Importance, &p.Strength, &p.AccessCount, &p.SupersededBy, &p.CreatedAt,
			&p.LastAccessedAt, &p.LastConsolidatedAt, &p.ContentMd5); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func gramCount(t *testing.T, db store.DB, id int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM trigrams WHERE memory_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func ftsContent(t *testing.T, db store.DB, id int64) string {
	t.Helper()
	var c string
	err := db.QueryRow(`SELECT content FROM memory_fts WHERE rowid = ?`, id).Scan(&c)
	if err != nil {
		return ""
	}
	return c
}

func learn(t *testing.T, db store.DB, cwd, content string, extra map[string]any) (string, *remdom.Memory, bool) {
	t.Helper()
	in := LearnInput{
		Content:    content,
		Kind:       strOf(extra, "kind", "fact"),
		Importance: numOf(extra, "importance", importanceDefault),
		Scope:      strOf(extra, "scope", ""),
		Source:     strOf(extra, "source", ""),
		Supersedes: idsOf(extra, "supersedes"),
	}
	if _, ok := extra["importance"]; ok {
		in.ImportanceSet = true
	}
	reply, mem, existing, err := Learn(context.Background(), db, cwd, in)
	if err != nil {
		t.Fatalf("learn %q: %v", content, err)
	}
	return reply, mem, existing
}

func strOf(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return def
}

func numOf(m map[string]any, key string, def float64) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return def
}

func idsOf(m map[string]any, key string) []int64 {
	switch v := m[key].(type) {
	case []int64:
		return v
	case int64:
		return []int64{v}
	}
	return nil
}

func ageTo(t *testing.T, db store.DB, id int64, daysAgo int) {
	t.Helper()
	old := time.Now().UTC().AddDate(0, 0, -daysAgo).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE memories SET last_consolidated_at = ? WHERE id = ?`, old, id); err != nil {
		t.Fatal(err)
	}
}

func ageCreated(t *testing.T, db store.DB, id int64, daysAgo int) {
	t.Helper()
	old := time.Now().UTC().AddDate(0, 0, -daysAgo).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE memories SET created_at = ? WHERE id = ?`, old, id); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptStoreQuarantinesAndReadsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rem.sqlite")
	if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, quarantined, err := store.Open(path, Statements(), SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if quarantined == "" || !strings.Contains(quarantined, ".corrupt-") {
		t.Fatalf("quarantined = %q, want the .corrupt- aside named", quarantined)
	}
	var leftover bool
	for _, f := range mustReadDir(t, dir) {
		if strings.Contains(f, ".corrupt-") {
			leftover = true
		}
	}
	if !leftover {
		t.Fatal("no quarantined file beside the fresh store")
	}
	reply, hits, err := Recall(context.Background(), db, "/ws1", RecallInput{K: 10})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(hits) != 0 || !strings.Contains(reply, "(no memories)") {
		t.Fatalf("recall over the fresh store: %q", reply)
	}
}

func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestLearnStoresContentWithScopeKindImportance(t *testing.T) {
	db := newDB(t)
	cwd := "/ws1"
	reply, mem, existing := learn(t, db, cwd, "the api returns 429 when the token expires",
		map[string]any{"kind": "constraint", "importance": 0.8})
	if existing || mem == nil {
		t.Fatal("fresh learn reported existing")
	}
	if !strings.Contains(reply, "learned m"+fmt.Sprint(mem.Id)) {
		t.Fatalf("reply %q", reply)
	}
	if got := memRow(t, db, mem.Content); got == nil {
		t.Fatal("memory absent")
	} else {
		if got.Scope != shortHash(cwd) {
			t.Errorf("scope %q, want %q", got.Scope, shortHash(cwd))
		}
		if got.ScopeLabel != "ws1" {
			t.Errorf("scope_label %q, want ws1", got.ScopeLabel)
		}
		if got.Kind != "constraint" || got.Importance != 0.8 || got.Strength != 0.8 {
			t.Errorf("row %+v", got)
		}

		if got.Source != nil {
			t.Errorf("source %v, want NULL without an explicit source", got.Source)
		}
	}
	if c := ftsContent(t, db, mem.Id); c != mem.Content {
		t.Fatalf("fts row %q", c)
	}
	if gramCount(t, db, mem.Id) == 0 {
		t.Fatal("no trigrams landed")
	}
}

func TestLearnIsIdempotentOnScopeContent(t *testing.T) {
	db := newDB(t)
	cwd := "/ws1"
	_, m1, _ := learn(t, db, cwd, "dup me", nil)
	_, m2, existing := learn(t, db, cwd, "dup me", nil)
	if !existing || m2.Id != m1.Id {
		t.Fatalf("second learn id %d existing %v, want first id %d", m2.Id, existing, m1.Id)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM memories WHERE content = ?`, "dup me").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestReLearnAcceptsImportanceUpdate(t *testing.T) {
	db := newDB(t)
	cwd := "/ws1"
	_, m1, _ := learn(t, db, cwd, "upd me", nil)
	reply, m2, existing := learn(t, db, cwd, "upd me", map[string]any{"importance": 0.2})
	if !existing || m2.Id != m1.Id {
		t.Fatal("re-learn did not hit the existing row")
	}
	if !strings.Contains(reply, "importance → 0.2") {
		t.Fatalf("reply %q", reply)
	}
	got := memRow(t, db, "upd me")
	if got.Importance != 0.2 {
		t.Errorf("importance %v, want 0.2", got.Importance)
	}
	if got.Strength != 0.5 {
		t.Errorf("strength %v, want untouched 0.5", got.Strength)
	}
}

func TestLearnSupersedesRefusesMissingTarget(t *testing.T) {
	db := newDB(t)
	cwd := "/ws1"
	_, old, _ := learn(t, db, cwd, "old way", nil)
	_, fresh, _ := learn(t, db, cwd, "new way", map[string]any{"supersedes": old.Id})
	if got := memRow(t, db, "old way"); got.SupersededBy == nil || *got.SupersededBy != fresh.Id {
		t.Fatalf("superseded_by = %v, want %d", got.SupersededBy, fresh.Id)
	}
	if got := memRow(t, db, "new way"); got.SupersededBy != nil {
		t.Fatal("the new one must not be superseded")
	}

	if _, _, _, err := Learn(context.Background(), db, cwd, LearnInput{
		Content: "another way", Supersedes: []int64{999},
	}); err == nil {
		t.Fatal("missing supersedes target did not refuse")
	} else if want := fmt.Sprintf("rem: supersedes target m%d not found", 999); err.Error() != want {
		t.Fatalf("voice:\n%q\nwant\n%q", err.Error(), want)
	}
	if got := memRow(t, db, "another way"); got != nil {
		t.Fatal("refused learn still landed")
	}
}

func TestLearnScopeGlobal(t *testing.T) {
	db := newDB(t)
	_, mem, _ := learn(t, db, "/ws2", "global fact about agents", map[string]any{"scope": "global"})
	if mem.Scope != "global" || mem.ScopeLabel != "global" {
		t.Fatalf("scope %q label %q", mem.Scope, mem.ScopeLabel)
	}
}

func TestLearnRefusesScopeAllAtExecute(t *testing.T) {
	db := newDB(t)
	if _, _, _, err := Learn(context.Background(), db, "/ws1", LearnInput{Content: "x", Scope: "all"}); err == nil {
		t.Fatal("scope=all learn succeeded")
	} else if want := "rem: scope must be project or global, got 'all'"; err.Error() != want {
		t.Fatalf("voice:\n%q", err.Error())
	}
}

func TestExplicitSourceLandsVerbatim(t *testing.T) {
	db := newDB(t)
	if _, mem, _ := learn(t, db, "/ws1", "attributed source", map[string]any{"source": "log: explicit"}); mem == nil {
		t.Fatal("explicit-source learn did not land")
	}
	if got := memRow(t, db, "attributed source"); got.Source == nil || *got.Source != "log: explicit" {
		t.Fatalf("explicit source %v", got.Source)
	}
}

func TestPerProjectMemoriesIsolatedByCwdScope(t *testing.T) {
	db := newDB(t)
	_, m1, _ := learn(t, db, "/ws1", "only ws1", nil)
	_, m2, _ := learn(t, db, "/ws2", "only ws1", we())
	if m1.Id == m2.Id {
		t.Fatal("distinct scopes landed the same id")
	}
	if m1.ScopeLabel != "ws1" || m2.ScopeLabel != "ws2" {
		t.Fatalf("labels %q %q", m1.ScopeLabel, m2.ScopeLabel)
	}
}

func we() map[string]any { return map[string]any{} }

func TestSelfSupersedesIsNoOp(t *testing.T) {
	db := newDB(t)
	_, m1, _ := learn(t, db, "/ws1", "self ref", nil)
	reply, m2, existing := learn(t, db, "/ws1", "self ref", map[string]any{"supersedes": m1.Id})
	if !existing || m2.Id != m1.Id {
		t.Fatal("self-supersedes did not hit the existing row")
	}
	if got := memRow(t, db, "self ref"); got.SupersededBy != nil {
		t.Fatalf("self-demotion: superseded_by %v", got.SupersededBy)
	}
	if !strings.Contains(reply, "already known") {
		t.Fatalf("reply %q", reply)
	}
}

func TestRecallProseIntentBothArms(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "the api returns 429 when the token expires", nil)
	reply, hits, err := Recall(context.Background(), db, "/ws1", RecallInput{Query: "token expires", K: 10})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(hits) != 1 || hits[0].Content != "the api returns 429 when the token expires" {
		t.Fatalf("hits %+v reply %q", hits, reply)
	}
	if hits[0].Match != "both" {
		t.Fatalf("match %q, want both", hits[0].Match)
	}
}

func TestReservedFtsOperatorsDoNotBreakGrammar(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "to be or not to be", nil)
	_, hits, err := Recall(context.Background(), db, "/ws1", RecallInput{Query: "be or not", K: 10})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !hasContent(t, hits, "to be or not to be") {
		t.Fatalf("hits %+v", hits)
	}
}

func hasContent(t *testing.T, hits []Hit, content string) bool {
	t.Helper()
	for _, h := range hits {
		if h.Content == content {
			return true
		}
	}
	return false
}

func TestNoMatchRendersEmpty(t *testing.T) {
	db := newDB(t)
	reply, hits, err := Recall(context.Background(), db, "/ws1", RecallInput{Query: "zzzznope", K: 10})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(hits) != 0 || !strings.Contains(reply, "(no memories)") {
		t.Fatalf("reply %q", reply)
	}
}

func TestIdentifierCorpusLlamaswapFuzzy(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "llama-swap on :8090 OOMs at depth", map[string]any{"kind": "solution"})
	_, hits, err := Recall(context.Background(), db, "/ws1", RecallInput{Query: "llamaswap", K: 10})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	hit := findContent(t, hits, "llama-swap")
	if hit == nil {
		t.Fatalf("llamaswap must reach the llama-swap memory: %+v", hits)
	}
	if hit.Match != "fuzzy" {
		t.Fatalf("match %q, want fuzzy", hit.Match)
	}
}

func findContent(t *testing.T, hits []Hit, sub string) *Hit {
	t.Helper()
	for i := range hits {
		if strings.Contains(hits[i].Content, sub) {
			return &hits[i]
		}
	}
	return nil
}

func TestIdentifierCorpusUdIq1sBoth(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "UD-IQ1_S quant of DeepSeek-V4-Flash", nil)
	_, hits, _ := Recall(context.Background(), db, "/ws1", RecallInput{Query: "UD IQ1S", K: 10})
	hit := findContent(t, hits, "UD-IQ1_S")
	if hit == nil {
		t.Fatalf("UD IQ1S must reach the UD-IQ1_S memory: %+v", hits)
	}
	if hit.Match != "both" {
		t.Fatalf("match %q, want both", hit.Match)
	}
}

func TestIdentifierCorpusNCpuMoeBoth(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "--n-cpu-moe 19 OOMs at depth", nil)
	_, hits, _ := Recall(context.Background(), db, "/ws1", RecallInput{Query: "n-cpu-moe", K: 10})
	hit := findContent(t, hits, "n-cpu-moe")
	if hit == nil {
		t.Fatalf("n-cpu-moe must reach the flag memory: %+v", hits)
	}
	if hit.Match != "both" {
		t.Fatalf("match %q, want both", hit.Match)
	}
}

func TestFtsLessBuildServesFuzzyFallback(t *testing.T) {
	db := newDB(t)
	off := false
	SetFtsAvailable(&off)
	defer SetFtsAvailable(nil)
	learn(t, db, "/ws1", "UD-IQ1_S quant of DeepSeek-V4-Flash", nil)
	_, hits, err := Recall(context.Background(), db, "/ws1", RecallInput{Query: "UD IQ1S", K: 10})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	hit := findContent(t, hits, "UD-IQ1_S")
	if hit == nil {
		t.Fatalf("fuzzy fallback must reach: %+v", hits)
	}
	if hit.Match != "fuzzy" {
		t.Fatalf("match %q, want fuzzy", hit.Match)
	}

	_, mem, _ := learn(t, db, "/ws1", "ftsless newcomer", nil)
	if got := memRow(t, db, "ftsless newcomer"); got == nil {
		t.Fatal("ftsless learn did not land")
	}
	if ftsContent(t, db, mem.Id) != "" {
		t.Fatal("fts row landed on an fts-less build")
	}
	_, hits2, _ := Recall(context.Background(), db, "/ws1", RecallInput{Query: "ftsless", K: 10})
	if !hasContent(t, hits2, "ftsless newcomer") {
		t.Fatalf("ftsless recall must serve: %+v", hits2)
	}
}

func TestStemmingPathReachesInflectedMemory(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "running fast", nil)
	_, hits, err := Recall(context.Background(), db, "/ws1", RecallInput{Query: "run fast", K: 10})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !hasContent(t, hits, "running fast") {
		t.Fatalf("stemming must reach: %+v", hits)
	}
}

func TestRecallKCapsLiveHits(t *testing.T) {
	db := newDB(t)
	for i := 1; i <= 5; i++ {
		learn(t, db, "/ws1", fmt.Sprintf("pattern %d", i), nil)
	}
	_, hits, err := Recall(context.Background(), db, "/ws1", RecallInput{Query: "pattern", K: 2})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want the cap of 2", len(hits))
	}
}

func TestAgedMemoryLosesToFresh(t *testing.T) {
	db := newDB(t)
	_, aged, _ := learn(t, db, "/ws1", "aged fix for widget", nil)
	ageTo(t, db, aged.Id, 40)
	learn(t, db, "/ws1", "fresh fix for widget", nil)
	_, hits, err := Recall(context.Background(), db, "/ws1", RecallInput{Query: "widget fix", K: 10})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	fresh := findContent(t, hits, "fresh fix")
	agedHit := findContent(t, hits, "aged fix")
	if fresh == nil || agedHit == nil {
		t.Fatalf("hits %+v", hits)
	}
	if !(fresh.EffectiveStrength > agedHit.EffectiveStrength) {
		t.Fatalf("fresh %v !> aged %v", fresh.EffectiveStrength, agedHit.EffectiveStrength)
	}
	if !(agedHit.EffectiveStrength < 0.3) {
		t.Fatalf("aged %v, want decay observable (< 0.3)", agedHit.EffectiveStrength)
	}
	row := memRow(t, db, "aged fix for widget")
	if row.Strength != 0.5 {
		t.Errorf("stored strength %v, want untouched 0.5", row.Strength)
	}
	if row.AccessCount != 1 || row.LastAccessedAt == nil {
		t.Errorf("access counters %+v", row)
	}
}

func TestSupersededDropByDefaultIncludeFills(t *testing.T) {
	db := newDB(t)
	_, old, _ := learn(t, db, "/ws1", "old approach to widgets", nil)
	_, _, _ = learn(t, db, "/ws1", "new approach to widgets", map[string]any{"supersedes": old.Id})
	_, hits, _ := Recall(context.Background(), db, "/ws1", RecallInput{Query: "widgets approach", K: 10})
	if len(hits) != 1 || hits[0].Content != "new approach to widgets" {
		t.Fatalf("hits %+v", hits)
	}
	_, hits2, _ := Recall(context.Background(), db, "/ws1",
		RecallInput{Query: "widgets approach", K: 10, IncludeSuperseded: true})
	if len(hits2) != 2 {
		t.Fatalf("include_superseded hits = %d, want 2", len(hits2))
	}
	oldHit := findContent(t, hits2, "old approach")
	newHit := findContent(t, hits2, "new approach")
	if oldHit == nil || newHit == nil || oldHit.SupersededBy == nil || *oldHit.SupersededBy != newHit.ID {
		t.Fatalf("hits2 %+v", hits2)
	}
}

func TestHybridScopeProjectFirstGlobalFill(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws2", "global widget lore", map[string]any{"scope": "global"})
	learn(t, db, "/ws2", "global widget warning", map[string]any{"scope": "global"})
	learn(t, db, "/ws1", "local widget fact", nil)
	_, hits, _ := Recall(context.Background(), db, "/ws1", RecallInput{Query: "widget", K: 10})
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].ScopeLabel != "ws1" {
		t.Fatalf("first hit %q, want the project scope first", hits[0].ScopeLabel)
	}
	var filled bool
	for _, h := range hits {
		if h.ScopeLabel == "global" {
			filled = true
		}
	}
	if !filled {
		t.Fatalf("global fill absent: %+v", hits)
	}

	_, hits2, _ := Recall(context.Background(), db, "/ws1", RecallInput{Query: "widget", K: 1})
	if len(hits2) != 1 || hits2[0].ScopeLabel != "ws1" {
		t.Fatalf("hits2 %+v", hits2)
	}
}

func TestScopeAllInterleavesScopes(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "widget local", nil)
	learn(t, db, "/ws1", "widget global", map[string]any{"scope": "global"})
	_, all, _ := Recall(context.Background(), db, "/ws1", RecallInput{Query: "widget", K: 10, Scope: "all"})
	var sawLocal, sawGlobal bool
	for _, h := range all {
		if h.ScopeLabel == "ws1" {
			sawLocal = true
		}
		if h.ScopeLabel == "global" {
			sawGlobal = true
		}
	}
	if !sawLocal || !sawGlobal {
		t.Fatalf("all-scope hits %+v", all)
	}
	_, globs, _ := Recall(context.Background(), db, "/ws1", RecallInput{Query: "widget", K: 10, Scope: "global"})
	if len(globs) == 0 {
		t.Fatal("global-scope recall empty")
	}
	for _, h := range globs {
		if h.ScopeLabel != "global" {
			t.Fatalf("global scope leaked %q", h.ScopeLabel)
		}
	}
}

func TestKindFilterNarrowsRecall(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "widget is blue", map[string]any{"kind": "constraint"})
	learn(t, db, "/ws1", "widget is green", map[string]any{"kind": "solution"})
	_, hits, _ := Recall(context.Background(), db, "/ws1",
		RecallInput{Query: "widget", K: 10, Kind: "solution"})
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	for _, h := range hits {
		if h.Kind != "solution" {
			t.Fatalf("kind filter leaked %q", h.Kind)
		}
	}
}

func TestEmptyQueryBrowsesLatestByEffectiveStrength(t *testing.T) {
	db := newDB(t)
	for i := 1; i <= 3; i++ {
		learn(t, db, "/ws1", fmt.Sprintf("browse probe %d", i), nil)
	}
	_, hits, err := Recall(context.Background(), db, "/ws1", RecallInput{K: 10})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("browse empty")
	}
	for i, h := range hits {
		if h.Match != "browse" {
			t.Fatalf("hit %d match %q, want browse", i, h.Match)
		}
		if i > 0 && hits[i].EffectiveStrength > hits[i-1].EffectiveStrength {
			t.Fatalf("browse not strength-ordered: %v > %v", hits[i].EffectiveStrength, hits[i-1].EffectiveStrength)
		}
	}
}

func TestReflectStoresDistilledWithSource(t *testing.T) {
	db := newDB(t)
	reply, mem, existing, err := Reflect(context.Background(), db, "/ws1", ReflectInput{
		Content:    "the write path never leaves a stale tmp file",
		Source:     "log: debugged 4 hours over the todo store",
		Importance: 0.3,
	})
	if err != nil {
		t.Fatalf("reflect: %v", err)
	}
	if existing || mem == nil {
		t.Fatal("fresh reflect reported existing")
	}
	if !strings.Contains(reply, "reflected m") {
		t.Fatalf("reply %q", reply)
	}
	got := memRow(t, db, mem.Content)
	if got.Kind != "reflection" {
		t.Fatalf("kind %q", got.Kind)
	}
	if got.Source == nil || *got.Source != "log: debugged 4 hours over the todo store" {
		t.Fatalf("source %v", got.Source)
	}
}

func TestReflectIsIdempotent(t *testing.T) {
	db := newDB(t)
	_, m1, _ := reflectIn(t, db, "/ws1", "repeatable reflection", 0.7)
	_, m2, existing := reflectIn(t, db, "/ws1", "repeatable reflection", 0.7)
	if !existing || m2.Id != m1.Id {
		t.Fatalf("reflect id %d existing %v", m2.Id, existing)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM memories WHERE content = ?`, "repeatable reflection").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func reflectIn(t *testing.T, db store.DB, cwd, content string, importance float64) (string, *remdom.Memory, bool) {
	t.Helper()
	reply, mem, existing, err := Reflect(context.Background(), db, cwd, ReflectInput{
		Content: content, Importance: importance, Source: "",
	})
	if err != nil {
		t.Fatalf("reflect: %v", err)
	}
	return reply, mem, existing
}

func TestAutoReflectDedupesAndIgnoresBadEvents(t *testing.T) {
	db := newDB(t)
	cwd := "/ws1"
	summary := "compacted session: fixed the render path and its tests"
	if _, err := AutoReflect(context.Background(), db, cwd, summary); err != nil {
		t.Fatalf("auto-reflect: %v", err)
	}
	if _, err := AutoReflect(context.Background(), db, cwd, summary); err != nil {
		t.Fatalf("replay: %v", err)
	}
	got := memRow(t, db, summary)
	if got == nil {
		t.Fatal("compaction reflection absent")
	}
	if got.Kind != "reflection" {
		t.Fatalf("kind %q", got.Kind)
	}
	if got.Importance != autoReflectionImportance {
		t.Fatalf("importance %v, want %v", got.Importance, autoReflectionImportance)
	}
	if got.Scope != shortHash(cwd) {
		t.Fatalf("scope %q", got.Scope)
	}
	if got.Source == nil || !strings.Contains(*got.Source, "compaction") {
		t.Fatalf("source %v", got.Source)
	}

	if _, err := AutoReflect(context.Background(), db, cwd, ""); err != nil {
		t.Fatalf("empty summary: %v", err)
	}
	if _, err := AutoReflect(context.Background(), db, cwd, "   "); err != nil {
		t.Fatalf("blank summary: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM memories`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("memories = %d, want the single deduped one", n)
	}
}

func TestPruneConsolidateIdempotentDecaysAged(t *testing.T) {
	db := newDB(t)
	_, mem, _ := learn(t, db, "/ws1", "consolidate me", nil)
	ageTo(t, db, mem.Id, 40)
	reply, count, err := Prune(context.Background(), db, "/ws1", PruneInput{Verb: "consolidate"})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if count < 1 || !strings.Contains(reply, "consolidated") {
		t.Fatalf("reply %q count %d", reply, count)
	}
	got := memRow(t, db, "consolidate me")
	if !(got.Strength < 0.3) {
		t.Fatalf("strength %v, want decay persisted (< 0.3)", got.Strength)
	}
	if got.AccessCount != 0 {
		t.Fatalf("access_count %d, want the window reset", got.AccessCount)
	}
	first := got.Strength
	if _, _, err := Prune(context.Background(), db, "/ws1", PruneInput{Verb: "consolidate"}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	twice := memRow(t, db, "consolidate me")
	if delta := twice.Strength - first; delta < -1e-6 || delta > 1e-6 {
		t.Fatalf("replay drifted %v -> %v", first, twice.Strength)
	}
}

func TestConsolidateReinforcesAccessedNoOpsFresh(t *testing.T) {
	db := newDB(t)
	learn(t, db, "/ws1", "fresh target", nil)
	if _, _, err := Recall(context.Background(), db, "/ws1", RecallInput{Query: "fresh", K: 10}); err != nil {
		t.Fatalf("recall: %v", err)
	}
	if _, _, err := Prune(context.Background(), db, "/ws1", PruneInput{Verb: "consolidate"}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	got := memRow(t, db, "fresh target")
	if !(got.Strength > 0.5 && got.Strength <= 0.6) {
		t.Fatalf("strength %v, want the reinforcement applied", got.Strength)
	}
}

func TestPruneRemoveNoOrphans(t *testing.T) {
	db := newDB(t)
	_, mem, _ := learn(t, db, "/ws1", "doomed memory", nil)
	reply, count, err := Prune(context.Background(), db, "/ws1", PruneInput{Verb: "remove", IDs: []int64{mem.Id}})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if count != 1 || !strings.Contains(reply, "removed 1") {
		t.Fatalf("reply %q count %d", reply, count)
	}
	if got := memRow(t, db, "doomed memory"); got != nil {
		t.Fatal("memory survived")
	}
	if c := ftsContent(t, db, mem.Id); c != "" {
		t.Fatalf("orphaned fts row %q", c)
	}
	if gramCount(t, db, mem.Id) != 0 {
		t.Fatal("orphaned trigram rows")
	}
}

func TestPruneRemoveByCriteria(t *testing.T) {
	db := newDB(t)
	_, constraint, _ := learn(t, db, "/ws1", "old constraint", map[string]any{"kind": "constraint"})
	ageCreated(t, db, constraint.Id, 10)
	_, fact, _ := learn(t, db, "/ws1", "old fact", nil)
	ageCreated(t, db, fact.Id, 10)
	reply, count, err := Prune(context.Background(), db, "/ws1",
		PruneInput{Verb: "remove", Kind: "constraint"})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if count != 1 || !strings.Contains(reply, "removed 1") {
		t.Fatalf("reply %q count %d", reply, count)
	}
	if got := memRow(t, db, "old constraint"); got != nil {
		t.Fatal("constraint survived")
	}
	if got := memRow(t, db, "old fact"); got == nil {
		t.Fatal("other kinds must survive")
	}
	learn(t, db, "/ws1", "young victim", nil)
	if _, _, err := Prune(context.Background(), db, "/ws1",
		PruneInput{Verb: "remove", OlderThanDays: 5}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if got := memRow(t, db, "young victim"); got == nil {
		t.Fatal("young memory pruned")
	}
}

func TestPruneRemoveMissingIdsReportZero(t *testing.T) {
	db := newDB(t)
	reply, count, err := Prune(context.Background(), db, "/ws1",
		PruneInput{Verb: "remove", IDs: []int64{999999}})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if count != 0 || !strings.Contains(reply, "removed 0") {
		t.Fatalf("reply %q count %d", reply, count)
	}
}

func TestPruneConsolidateHonorsSelection(t *testing.T) {
	db := newDB(t)
	_, victim, _ := learn(t, db, "/ws1", "narrow victim", nil)
	ageTo(t, db, victim.Id, 40)
	learn(t, db, "/ws1", "narrow bystander", nil)
	before := memRow(t, db, "narrow bystander").LastConsolidatedAt
	reply, count, err := Prune(context.Background(), db, "/ws1",
		PruneInput{Verb: "consolidate", IDs: []int64{victim.Id}})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if count != 1 || !strings.Contains(reply, "consolidated 1") {
		t.Fatalf("reply %q count %d", reply, count)
	}
	if got := memRow(t, db, "narrow victim"); !(got.Strength < 0.3) {
		t.Fatalf("selected strength %v", got.Strength)
	}
	if got := memRow(t, db, "narrow bystander"); got.Strength != 0.5 || got.LastConsolidatedAt != before {
		t.Fatalf("unselected row drifted: %+v", got)
	}
}

func TestPruneRemoveFtsLessNeverTouchesFts(t *testing.T) {
	db := newDB(t)
	if _, err := db.Exec(`DROP TABLE IF EXISTS memory_fts`); err != nil {
		t.Fatal(err)
	}
	off := false
	SetFtsAvailable(&off)
	defer SetFtsAvailable(nil)
	_, mem, _ := learn(t, db, "/ws1", "ftsless victim", nil)
	reply, count, err := Prune(context.Background(), db, "/ws1",
		PruneInput{Verb: "remove", IDs: []int64{mem.Id}})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if count != 1 || !strings.Contains(reply, "removed 1") {
		t.Fatalf("reply %q count %d", reply, count)
	}
	if got := memRow(t, db, "ftsless victim"); got != nil {
		t.Fatal("memory survived")
	}
	if gramCount(t, db, mem.Id) != 0 {
		t.Fatal("trigrams not cleaned")
	}
}

func TestPruneNoSelectionRefuses(t *testing.T) {
	db := newDB(t)
	for _, verb := range []string{"remove", "reduce"} {
		if _, _, err := Prune(context.Background(), db, "/ws1", PruneInput{Verb: verb}); err == nil {
			t.Fatalf("%s without selection succeeded", verb)
		} else if want := "rem: prune needs ids or criteria (kind/older_than_days/scope)"; err.Error() != want {
			t.Fatalf("%s voice:\n%q", verb, err.Error())
		}
	}
}

func TestPruneReduceRequiresImportance(t *testing.T) {
	db := newDB(t)
	_, mem, _ := learn(t, db, "/ws1", "reducable", map[string]any{"importance": 0.9})
	if _, _, err := Prune(context.Background(), db, "/ws1",
		PruneInput{Verb: "reduce", IDs: []int64{mem.Id}}); err == nil {
		t.Fatal("reduce without importance succeeded")
	} else if want := "rem: reduce needs an importance to lower to"; err.Error() != want {
		t.Fatalf("voice:\n%q", err.Error())
	}
	target := 0.2
	reply, count, err := Prune(context.Background(), db, "/ws1",
		PruneInput{Verb: "reduce", IDs: []int64{mem.Id}, Importance: &target})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if count != 1 || !strings.Contains(reply, "reduced 1 to 0.2") {
		t.Fatalf("reply %q count %d", reply, count)
	}
	if got := memRow(t, db, "reducable"); got.Importance != 0.2 {
		t.Fatalf("importance %v", got.Importance)
	}
}

func TestRemovingSupersedingUnsupersedesLegacy(t *testing.T) {
	db := newDB(t)
	_, old, _ := learn(t, db, "/ws1", "superseded legacy", nil)
	_, fresh, _ := learn(t, db, "/ws1", "superseding new", map[string]any{"supersedes": old.Id})
	if _, _, err := Prune(context.Background(), db, "/ws1",
		PruneInput{Verb: "remove", IDs: []int64{fresh.Id}}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	got := memRow(t, db, "superseded legacy")
	if got.SupersededBy != nil {
		t.Fatalf("superseded_by %v, want SET NULL", got.SupersededBy)
	}
	_, hits, _ := Recall(context.Background(), db, "/ws1", RecallInput{Query: "legacy", K: 10})
	if !hasContent(t, hits, "superseded legacy") {
		t.Fatalf("legacy must resurface: %+v", hits)
	}
}

func TestPrunedIdsNeverReused(t *testing.T) {
	db := newDB(t)
	_, victim, _ := learn(t, db, "/ws1", "highest id victim", nil)
	if _, _, err := Prune(context.Background(), db, "/ws1",
		PruneInput{Verb: "remove", IDs: []int64{victim.Id}}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	_, next, _ := learn(t, db, "/ws1", "id reuse probe", nil)
	if next.Id <= victim.Id {
		t.Fatalf("reused id %d <= %d", next.Id, victim.Id)
	}
}

func TestPruneRemoveByIdsIgnoresScope(t *testing.T) {
	db := newDB(t)
	_, glo, _ := learn(t, db, "/ws2", "global doomed", map[string]any{"scope": "global"})
	reply, count, err := Prune(context.Background(), db, "/ws1",
		PruneInput{Verb: "remove", IDs: []int64{glo.Id}})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if count != 1 || !strings.Contains(reply, "removed 1") {
		t.Fatalf("reply %q count %d", reply, count)
	}
	if got := memRow(t, db, "global doomed"); got != nil {
		t.Fatal("global memory survived")
	}
}

func TestConcurrentCallsSerialize(t *testing.T) {

	path := filepath.Join(t.TempDir(), "shared.sqlite")
	db1, _, err := store.Open(path, Statements(), SchemaVersion)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db2, _, err := store.Open(path, Statements(), SchemaVersion)
	if err != nil {
		t.Fatalf("open two: %v", err)
	}
	const (
		gor   = 10
		slice = 2
	)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for wi, db := range []store.DB{db1, db2} {
		for g := 0; g < gor; g++ {
			wg.Add(1)
			go func(wi, g int, db store.DB) {
				defer wg.Done()
				for i := 0; i < slice; i++ {
					_, _, _, err := Learn(context.Background(), db, "/ws1", LearnInput{
						Content: fmt.Sprintf("w%d-g%d-%d witness", wi, g, i),
					})
					if err != nil {
						mu.Lock()
						errs = append(errs, err)
						mu.Unlock()
					}
					if _, _, err := Recall(context.Background(), db, "/ws1",
						RecallInput{Query: "witness", K: 10}); err != nil {
						mu.Lock()
						errs = append(errs, err)
						mu.Unlock()
					}
				}
			}(wi, g, db)
		}
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("%d of %d concurrent calls failed: %v", len(errs), 2*gor*slice*2, errs[0])
	}
	var n int
	if err := db1.QueryRow(`SELECT count(*) FROM memories WHERE content GLOB '*witness*'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if want := 2 * gor * slice; n != want {
		t.Fatalf("witness memories = %d, want %d (no lost writes)", n, want)
	}
}

func TestGeneratedMatchesCommitted(t *testing.T) {
	liftCmd, err := filepath.Abs(filepath.Join(os.Getenv("HOME"), "Projects", "lift", "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(liftCmd, "main.go")); err != nil {
		t.Skip("lift checkout absent")
	}
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := os.MkdirTemp("", "remgen")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	regen := func() {
		genCfg := map[string]any{}
		if err := json.Unmarshal(mustRead(t, filepath.Join(workDir, "gen.json")), &genCfg); err != nil {
			t.Fatalf("gen.json: %v", err)
		}
		genCfg["name"] = tmp
		if err := writeJSON(t, filepath.Join(tmp, "gen.json"), genCfg); err != nil {
			t.Fatal(err)
		}
		srcCfg := map[string]any{}
		if err := json.Unmarshal(mustRead(t, filepath.Join(workDir, "source.json")), &srcCfg); err != nil {
			t.Fatalf("source.json: %v", err)
		}
		srcCfg["sourceDirectory"] = workDir
		if err := writeJSON(t, filepath.Join(tmp, "source.json"), srcCfg); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("go", "run", "main.go", "-config="+filepath.Join(tmp, "gen.json"), "-source="+filepath.Join(tmp, "source.json"))
		cmd.Dir = liftCmd
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("regeneration: %v\n%s", err, out)
		}
	}
	regen()

	regenCmd := "cd <lift>/cmd && go run main.go -config=$RIG/store/rem/gen.json -source=$RIG/store/rem/source.json"
	for _, pkg := range []string{"domain", "ddl"} {
		committed := listFiles(t, filepath.Join(workDir, pkg))
		got := listFiles(t, filepath.Join(tmp, pkg))
		var missing []string
		for name, cb := range committed {
			b, ok := got[name]
			if !ok {
				missing = append(missing, pkg+"/"+name)
				continue
			}
			if !stringsEqual(cb, b) {
				t.Fatalf("drift in %s/%s (regenerate: %s)", pkg, name, regenCmd)
			}
		}
		if len(missing) != 0 {
			sort.Strings(missing)
			t.Fatalf("missing generated files %v (regenerate: %s)", missing, regenCmd)
		}
	}
}

func stringsEqual(a, b []byte) bool {
	return len(a) == len(b) && hex.EncodeToString(a) == hex.EncodeToString(b)
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func writeJSON(t *testing.T, path string, v any) error {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func listFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out[rel] = b
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

func TestExtraAndFtsStatementsAreApplied(t *testing.T) {
	statements := Statements()
	if len(statements) <= len(DDL()) {
		t.Fatalf("Statements() = %d statements, want the generated DDL plus the extras", len(statements))
	}
	joined := strings.Join(statements, "\n")
	for _, want := range []string{
		"memories_scope_content",
		"memories_scope_created",
		"trigrams_gram_idx",
		"trigrams_memory_idx",
		"memory_fts",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("statement absent from the applied set (%s)", want)
		}
	}
	db := newDB(t)
	var indexes, fts int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('memories_scope_content','memories_scope_created','trigrams_gram_idx','trigrams_memory_idx')`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes != 4 {
		t.Fatalf("indexes after open = %d, want 4", indexes)
	}
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='memory_fts'`).Scan(&fts); err != nil {
		t.Fatal(err)
	}
	if fts != 1 {
		t.Fatalf("memory_fts after open = %d, want 1", fts)
	}
}

func TestRecentIsTheCwdsNewestLiveNotes(t *testing.T) {
	db := newDB(t)
	cwd := "/ws/recent"
	for i := 1; i <= 10; i++ {
		learn(t, db, cwd, fmt.Sprintf("note %02d", i), we())
	}
	learn(t, db, "/ws/other", "foreign note", we())
	learn(t, db, cwd, "global-ish note", map[string]any{"scope": "global"})
	notes, err := Recent(context.Background(), db, cwd, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 8 {
		t.Fatalf("K=8 caps the read, got %d: %v", len(notes), notes)
	}
	want := []string{"note 10", "note 09", "note 08", "note 07", "note 06", "note 05", "note 04", "note 03"}
	for i := range want {
		if notes[i] != want[i] {
			t.Fatalf("newest first, newest K only, got: %v", notes)
		}
	}
	joined := strings.Join(notes, "\n")
	if strings.Contains(joined, "foreign note") || strings.Contains(joined, "global-ish note") {
		t.Fatalf("another scope's notes must not ride: %v", notes)
	}
}

func TestRecentSkipsSuperseded(t *testing.T) {
	db := newDB(t)
	cwd := "/ws/super"
	learn(t, db, cwd, "old fact", we())
	learn(t, db, cwd, "new fact", map[string]any{"supersedes": int64(1)})
	notes, err := Recent(context.Background(), db, cwd, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0] != "new fact" {
		t.Fatalf("the superseded note must not ride, got: %v", notes)
	}
}

func TestRecentEmptyStoreIsAbsent(t *testing.T) {
	db := newDB(t)
	notes, err := Recent(context.Background(), db, "/ws/empty", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Fatalf("an empty store reads empty, got: %v", notes)
	}
}
