package todo_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/store"
	todostore "github.com/mrsirg97-rgb/rig/store/todo"
)

func completeText(t *testing.T, db store.DB, id string) {
	t.Helper()
	if _, err := todostore.Complete(context.Background(), db, p, id, "s1", false); err != nil {
		t.Fatalf("complete %s: %v", id, err)
	}
}

// The present chain: root pending requires dep3, dep3 done requires dep2,
// dep2 done requires dep1, dep1 done. Fourteen unrelated done follow.
func presentQueue(t *testing.T, db store.DB) string {
	t.Helper()
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{
		{Text: "dep1"},
		{Text: "dep2", Requires: ptrTo("dep1")},
		{Text: "dep3", Requires: ptrTo("dep2")},
		{Text: "root", Requires: ptrTo("dep3")},
		{Text: "r1"},
		{Text: "r2"},
		{Text: "r3"},
		{Text: "r4"},
		{Text: "r5"},
		{Text: "r6"},
		{Text: "r7"},
		{Text: "r8"},
		{Text: "r9"},
		{Text: "r10"},
		{Text: "r11"},
		{Text: "r12"},
		{Text: "r13"},
		{Text: "r14"},
	}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, text := range []string{"dep1", "dep2", "dep3"} {
		completeText(t, db, taskIDText(t, reply, text))
	}
	for i := 1; i <= 14; i++ {
		completeText(t, db, taskIDText(t, reply, "r"+itoa(i)))
	}
	read, err := todostore.Read(ctx, db, p, "s1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return read
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestReadDefaultIsThePresent(t *testing.T) {
	got := presentQueue(t, newDB(t))
	want := "[ws] 1 open · 10 of 17 finished shown · next: t4\n" +
		"  t4 [ ] root · requires t3\n" +
		"  t3 [x] dep3 · requires t2\n" +
		"  t2 [x] dep2 · requires t1\n" +
		"  t1 [x] dep1\n" +
		"  t18 [x] r14\n" +
		"  t17 [x] r13\n" +
		"  t16 [x] r12\n" +
		"  t15 [x] r11\n" +
		"  t14 [x] r10\n" +
		"  t13 [x] r9\n" +
		"  t12 [x] r8\n" +
		"· 7 more finished · todo list finished 10"
	if got != want {
		t.Fatalf("the present must render open work, then the related chain, then recent finished:\n%s\nwant:\n%s", got, want)
	}
}

func TestReadDefaultRelatedChainsCapAtTheTenNearest(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	items := []item{{Text: "d1"}}
	for i := 2; i <= 12; i++ {
		items = append(items, item{Text: "d" + itoa(i), Requires: ptrTo("d" + itoa(i-1))})
	}
	items = append(items, item{Text: "root", Requires: ptrTo("d12")})
	reply, err := todostore.Create(ctx, db, p, items, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 1; i <= 12; i++ {
		completeText(t, db, taskIDText(t, reply, "d"+itoa(i)))
	}
	read, err := todostore.Read(ctx, db, p, "s1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "[ws] 1 open · 10 of 12 finished shown · next: t13") {
		t.Fatalf("the head must name the cap:\n%s", read)
	}
	if !strings.Contains(read, "· 2 more finished · todo list finished 10") {
		t.Fatalf("the hint must name the hidden two:\n%s", read)
	}
	if !strings.Contains(read, "  t12 [x] d12 · requires t11\n  t11 [x] d11 · requires t10") {
		t.Fatalf("the nearest hop leads:\n%s", read)
	}
	if !strings.Contains(read, "  t4 [x] d4 · requires t3\n  t3 [x] d3 · requires t2\n\u00b7 2 more finished") {
		t.Fatalf("the tenth nearest hop closes the shown set:\n%s", read)
	}
	if strings.Contains(read, "[x] d1\n") || strings.Contains(read, "[x] d2\n") {
		t.Fatalf("the two farthest hops must stay hidden:\n%s", read)
	}
}

func TestReadFinishedListsNewestFirst(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	reply, err := todostore.Create(ctx, db, p, []item{
		{Text: "a"},
		{Text: "b"},
		{Text: "c"},
		{Text: "work"},
	}, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	completeText(t, db, taskIDText(t, reply, "a"))
	completeText(t, db, taskIDText(t, reply, "b"))
	completeText(t, db, taskIDText(t, reply, "c"))
	got, err := todostore.ReadFinished(ctx, db, p, "s1", 2)
	if err != nil {
		t.Fatalf("read finished: %v", err)
	}
	want := "[ws] 1 open · 2 of 3 finished shown · next: t4\n" +
		"  t3 [x] c\n" +
		"  t2 [x] b\n" +
		"· 1 more finished · todo list finished 2"
	if got != want {
		t.Fatalf("the finished list must be newest first:\n%s\nwant:\n%s", got, want)
	}
}

func TestReadFinishedDefaultsToTenAndCapsAtOneHundred(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	items := []item{}
	for i := 1; i <= 15; i++ {
		items = append(items, item{Text: "done " + itoa(i)})
	}
	items = append(items, item{Text: "work"})
	reply, err := todostore.Create(ctx, db, p, items, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 1; i <= 15; i++ {
		completeText(t, db, taskIDText(t, reply, "done "+itoa(i)))
	}
	got, err := todostore.ReadFinished(ctx, db, p, "s1", 0)
	if err != nil {
		t.Fatalf("read finished default: %v", err)
	}
	if !strings.Contains(got, "[ws] 1 open · 10 of 15 finished shown") || !strings.Contains(got, "· 5 more finished · todo list finished 10") {
		t.Fatalf("the default finished list must show ten:\n%s", got)
	}
	if _, err := todostore.ReadFinished(ctx, db, p, "s1", todostore.FinishedListCap+1); err == nil ||
		!strings.Contains(err.Error(), "1-100") {
		t.Fatalf("over the cap must refuse naming the range, got %v", err)
	}
}

func TestHiddenFinishedStillSatisfiesRequires(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	items := []item{{Text: "d1"}}
	for i := 2; i <= 12; i++ {
		items = append(items, item{Text: "d" + itoa(i), Requires: ptrTo("d" + itoa(i-1))})
	}
	items = append(items, item{Text: "root", Requires: ptrTo("d12")})
	reply, err := todostore.Create(ctx, db, p, items, "s1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 1; i <= 12; i++ {
		completeText(t, db, taskIDText(t, reply, "d"+itoa(i)))
	}
	// A failed task beyond the ten nearest hops, requiring the hidden d1.
	extra, err := todostore.Create(ctx, db, p, []item{{Text: "later", Requires: ptrTo("d1")}}, "s1")
	if err != nil {
		t.Fatalf("create later: %v", err)
	}
	later := taskIDText(t, extra, "later")
	if _, err := todostore.Start(ctx, db, p, later, "s1", false); err != nil {
		t.Fatalf("start later: %v", err)
	}
	if _, err := todostore.Fail(ctx, db, p, later, "s1", false); err != nil {
		t.Fatalf("fail later: %v", err)
	}
	read, err := todostore.Read(ctx, db, p, "s1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(read, "[x] d1\n") {
		t.Fatalf("d1 (the farthest hop) must be retired from the view:\n%s", read)
	}
	// Retirement is a view, not a state: the hidden d1 still satisfies
	// later's requires, links resolve by id, and the read/notes doors work.
	if _, err := todostore.Retry(ctx, db, p, later, "s1"); err != nil {
		t.Fatalf("retry the task that requires the hidden d1: %v", err)
	}
	if _, err := todostore.Start(ctx, db, p, later, "s1", false); err != nil {
		t.Fatalf("the hidden done dependency must still satisfy requires: %v", err)
	}
	if _, err := todostore.ReadOne(ctx, db, p, "t1", "s1"); err != nil {
		t.Fatalf("the hidden id must still resolve by id: %v", err)
	}
	if _, err := todostore.Notes(ctx, db, p, "t1", "s1"); err != nil {
		t.Fatalf("notes on the hidden id must work: %v", err)
	}
}
