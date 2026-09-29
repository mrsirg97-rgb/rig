package todo

import (
	"fmt"
	"sort"
	"strings"
)

const (
	statusPending = "pending"
	statusActive  = "in_progress"
	statusReview  = "review"
	statusDone    = "done"
	statusFailed  = "failed"
)

func marker(status string) string {
	switch status {
	case statusDone:
		return "[x]"
	case statusFailed:
		return "[!]"
	case statusReview:
		return "[r]"
	case statusActive:
		return "[~]"
	default:
		return "[ ]"
	}
}

func orderedTaskStates(f *folded) []*taskState {
	out := make([]*taskState, 0, len(f.tasks))
	for _, ts := range f.tasks {
		out = append(out, ts)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].pos != out[j].pos {
			return out[i].pos < out[j].pos
		}
		return out[i].createdSeq < out[j].createdSeq
	})
	return out
}

func blockersOf(f *folded, ts *taskState) []string {
	var out []string
	for _, ot := range orderedTaskStates(f) {
		if ot.id == ts.requires || ot.blocks == ts.id {
			if ot.status != statusDone {
				out = append(out, ot.id)
			}
		}
	}
	return out
}

func blockedBy(f *folded, ts *taskState) []string {
	if ts.status != statusPending && ts.status != statusActive && ts.status != statusReview {
		return nil
	}
	return blockersOf(f, ts)
}

func blockHint(f *folded, ids []string) string {
	statuses := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if ts := f.tasks[id]; ts != nil && !seen[ts.status] {
			seen[ts.status] = true
			statuses = append(statuses, ts.status)
		}
	}
	if len(statuses) == 1 {
		switch statuses[0] {
		case statusPending:
			if len(ids) == 1 {
				return "pending; start it first"
			}
			return "pending; start them first"
		case statusFailed:
			if len(ids) == 1 {
				return "failed; retry it first"
			}
			return "failed; retry them first"
		case statusReview:
			return "in review"
		default:
			return "in_progress"
		}
	}
	return strings.Join(statuses, ", ")
}

func blockedVoice(id string, ids []string) string {
	quoted := make([]string, len(ids))
	for i, b := range ids {
		quoted[i] = "'" + b + "'"
	}
	return fmt.Sprintf("'%s' is blocked by %s", id, strings.Join(quoted, ", "))
}

func claimSuffix(ts *taskState, session string) string {
	if ts.status != statusActive && ts.status != statusReview {
		return ""
	}
	if ts.owner == "" || ts.owner == session {
		return ""
	}
	owner := ts.owner
	if len(owner) > 8 {
		owner = owner[:8]
	}
	verb := "claimed by"
	if ts.status == statusReview {
		verb = "claimed for review by"
	}
	return " \u00b7 " + verb + " " + owner
}

func staleFooter(f *folded) string {
	if f.maxSeq <= STALE_THRESHOLD_SEQ {
		return ""
	}
	n := 0
	latest := ""
	for _, ts := range f.tasks {
		if ts.status != statusPending && ts.status != statusActive && ts.status != statusReview {
			continue
		}
		if ts.updatedSeq > f.maxSeq-STALE_THRESHOLD_SEQ {
			continue
		}
		n++
		if ts.updatedTs > latest {
			latest = ts.updatedTs
		}
	}
	if n == 0 {
		return ""
	}
	if len(latest) > 10 {
		latest = latest[:10]
	}
	return fmt.Sprintf("\u00b7 %d unresolved since %s (recovered from log)", n, latest)
}

func summaryOf(f *folded, shown int) string {
	ordered := orderedTaskStates(f)
	open, finished := 0, 0
	nextID := ""
	for _, ts := range ordered {
		if ts.status == statusDone {
			finished++
		} else {
			open++
		}
	}
	for _, ts := range ordered {
		if ts.status == statusPending && len(blockedBy(f, ts)) == 0 {
			nextID = ts.id
			break
		}
	}
	var b strings.Builder
	b.WriteString(scopeTag(f))
	fmt.Fprintf(&b, "%d open", open)
	if finished > 0 {
		fmt.Fprintf(&b, " \u00b7 %d of %d finished shown", shown, finished)
	}
	if nextID != "" {
		fmt.Fprintf(&b, " \u00b7 next: %s", nextID)
	}
	return b.String()
}

func isFinished(ts *taskState) bool {
	return ts.status == statusDone
}

func finishedCount(f *folded) int {
	n := 0
	for _, ts := range f.tasks {
		if isFinished(ts) {
			n++
		}
	}
	return n
}

func defaultShown(f *folded) int {
	if n := finishedCount(f); n < DefaultFinishedShown {
		return n
	}
	return DefaultFinishedShown
}

func relatedFinished(f *folded) []*taskState {
	adj := map[string][]string{}
	for _, ts := range f.tasks {
		for _, ref := range []string{ts.requires, ts.blocks} {
			if ref == "" || f.tasks[ref] == nil {
				continue
			}
			adj[ts.id] = append(adj[ts.id], ref)
			adj[ref] = append(adj[ref], ts.id)
		}
	}
	byOrder := map[string]*taskState{}
	for _, ts := range f.tasks {
		byOrder[ts.id] = ts
	}
	var queue []*taskState
	seen := map[string]bool{}
	for _, ts := range orderedTaskStates(f) {
		if !isFinished(ts) {
			seen[ts.id] = true
			queue = append(queue, ts)
		}
	}
	hop := func(a, b *taskState) bool {
		if a.pos != b.pos {
			return a.pos < b.pos
		}
		return a.createdSeq < b.createdSeq
	}
	var out []*taskState
	for i := 0; i < len(queue); i++ {
		ts := queue[i]
		if isFinished(ts) {
			out = append(out, ts)
		}
		nbrs := make([]*taskState, 0, len(adj[ts.id]))
		for _, id := range adj[ts.id] {
			nbrs = append(nbrs, byOrder[id])
		}
		sort.Slice(nbrs, func(a, b int) bool { return hop(nbrs[a], nbrs[b]) })
		for _, nb := range nbrs {
			if !seen[nb.id] {
				seen[nb.id] = true
				queue = append(queue, nb)
			}
		}
	}
	return out
}

func recentFinished(f *folded) []*taskState {
	var out []*taskState
	for _, ts := range f.tasks {
		if isFinished(ts) {
			out = append(out, ts)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].finishedSeq != out[j].finishedSeq {
			return out[i].finishedSeq > out[j].finishedSeq
		}
		if out[i].pos != out[j].pos {
			return out[i].pos < out[j].pos
		}
		return out[i].createdSeq < out[j].createdSeq
	})
	return out
}

func finishedRows(f *folded, n int, related bool) []*taskState {
	out := make([]*taskState, 0, n)
	seen := map[string]bool{}
	if related {
		for _, ts := range relatedFinished(f) {
			if len(out) >= n {
				break
			}
			out = append(out, ts)
			seen[ts.id] = true
		}
	}
	for _, ts := range recentFinished(f) {
		if len(out) >= n {
			break
		}
		if seen[ts.id] {
			continue
		}
		out = append(out, ts)
		seen[ts.id] = true
	}
	return out
}

func scopeTag(f *folded) string {
	if f.label == "" {
		return ""
	}
	return "[" + f.label + "] "
}

func waitersOf(f *folded, ts *taskState) int {
	n := 0
	for _, ot := range f.tasks {
		if ot.blocks == ts.id && ot.status != statusDone {
			n++
		}
	}
	return n
}

func lineOf(f *folded, ts *taskState, session string) string {
	line := fmt.Sprintf("  %s %s %s", ts.id, marker(ts.status), ts.text)
	if ts.requires != "" {
		line += " \u00b7 requires " + ts.requires
	}
	if ts.blocks != "" {
		line += " \u00b7 blocks " + ts.blocks
	}
	if n := waitersOf(f, ts); n != 0 {
		line += fmt.Sprintf(" \u00b7 waits for %d", n)
	}
	line += claimSuffix(ts, session)
	return line
}

func noteCountLine(ts *taskState, notePointer bool) string {
	if len(ts.notes) == 0 {
		return ""
	}
	n := len(ts.notes)
	word := "notes"
	if n == 1 {
		word = "note"
	}
	line := fmt.Sprintf("    \u00b7 %d %s", n, word)
	if notePointer {
		line += fmt.Sprintf(" (action 'notes' with id=%s lists them)", ts.id)
	}
	return line
}

func renderTask(f *folded, ts *taskState, session string) string {
	line := lineOf(f, ts, session)
	if count := noteCountLine(ts, false); count != "" {
		line += "\n" + count
	}
	return line
}

func renderOne(f *folded, ts *taskState, session string) string {
	line := lineOf(f, ts, session)
	if count := noteCountLine(ts, true); count != "" {
		line += "\n" + count
	}
	return line
}

type readMode int

const (
	modePresent readMode = iota
	modeAll
	modeFinished
)

func renderQueue(f *folded, session string, mode readMode, n int, label string) string {
	ordered := orderedTaskStates(f)
	if len(ordered) == 0 {
		return fmt.Sprintf("(no tasks in %s's queue)", label)
	}
	var rows []*taskState
	shown := 0
	switch mode {
	case modeAll:
		rows = ordered
		shown = finishedCount(f)
	case modeFinished:
		rows = finishedRows(f, n, false)
		shown = len(rows)
	default:
		for _, ts := range ordered {
			if !isFinished(ts) {
				rows = append(rows, ts)
			}
		}
		rows = append(rows, finishedRows(f, DefaultFinishedShown, true)...)
		for _, ts := range rows {
			if isFinished(ts) {
				shown++
			}
		}
	}
	var b strings.Builder
	b.WriteString(summaryOf(f, shown))
	for _, ts := range rows {
		b.WriteString("\n" + renderTask(f, ts, session))
	}
	if hidden := finishedCount(f) - shown; hidden > 0 {
		window := finishedCount(f)
		if window > FinishedListCap {
			window = FinishedListCap
		}
		fmt.Fprintf(&b, "\n\u00b7 %d more finished \u00b7 todo list finished %d", hidden, window)
	}
	return b.String()
}

func echoTask(f *folded, session, id, note string) string {
	var b strings.Builder
	if note != "" {
		fmt.Fprintf(&b, "\u2192 %s\n", note)
	}
	if ts := f.tasks[id]; ts != nil {
		b.WriteString(renderTask(f, ts, session))
	}
	b.WriteString("\n" + summaryOf(f, defaultShown(f)))
	if foot := staleFooter(f); foot != "" {
		b.WriteString("\n" + foot)
	}
	return b.String()
}
