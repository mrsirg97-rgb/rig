package decision

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

const maxCorrection = 1024

type ReviewRow struct {
	ID         int64
	Site       string
	State      string
	Question   Question
	Answer     string
	Confidence *float64
	Decider    string
}

type Reviews interface {
	Pending(ctx context.Context) ([]ReviewRow, error)
	Settle(ctx context.Context, id int64, approved bool, reviewer, answer string) error
}

type Fire func(ctx context.Context, prompt string) (string, error)

type Reviewer struct {
	wake     chan struct{}
	dirty    atomic.Bool
	reviews  Reviews
	fire     Fire
	reviewer string
	budget   int
	loud     func(string)
}

// NewReviewer wires the reviewer; budget is the reviewer model row's
// window minus its reserve, in tokens — the room one fire's prompt may
// take. Rows that do not fit stay pending for the next turn end.
func NewReviewer(reviews Reviews, fire Fire, reviewer string, budget int, loud func(string)) *Reviewer {
	return &Reviewer{
		wake:     make(chan struct{}, 1),
		reviews:  reviews,
		fire:     fire,
		reviewer: reviewer,
		budget:   budget,
		loud:     loud,
	}
}

// Land marks the reviewer dirty: a proposal landed in the store.
func (r *Reviewer) Land() { r.dirty.Store(true) }

// Wake nudges the reviewer at a turn end; the pass runs only when a
// landing has marked it dirty, so a turn end with nothing landed costs
// nothing.
func (r *Reviewer) Wake() {
	if !r.dirty.Load() {
		return
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// TurnEnds wraps a frontend so the session's turn end wakes the reviewer:
// only an interactive session reviews, and the wake is the turn end,
// never the landing.
func TurnEnds(fe core.Frontend, r *Reviewer) core.Frontend {
	return turnEndWake{inner: fe, rev: r}
}

type turnEndWake struct {
	inner core.Frontend
	rev   *Reviewer
}

func (w turnEndWake) Input(ctx context.Context) (string, error) { return w.inner.Input(ctx) }

func (w turnEndWake) Notify(ev core.Event) {
	w.inner.Notify(ev)
	if _, ok := ev.(core.TurnEnd); ok {
		w.rev.Wake()
	}
}

func (r *Reviewer) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
			r.dirty.Store(false) // the pass takes everything pending at its read
			if err := r.Drain(ctx); err != nil {
				r.say("decision: review: %v", err)
			}
		}
	}
}

func (r *Reviewer) Drain(ctx context.Context) error {
	all, err := r.reviews.Pending(ctx)
	if err != nil {
		return fmt.Errorf("pending: %w", err)
	}
	if len(all) == 0 {
		return nil
	}
	rows := fitRows(all, reviewContract, r.budget)
	if len(rows) < len(all) {
		r.dirty.Store(true) // the rest stay pending for the next turn end
	}
	out, err := r.fire(ctx, reviewPrompt(rows))
	if err != nil {
		return fmt.Errorf("fire: %w", err)
	}
	verdicts := parseVerdicts(out)
	settled := 0
	for _, row := range rows {
		v, ok := verdicts[row.ID]
		if !ok {
			continue
		}
		if !v.approved && v.answer == "" {
			continue
		}
		if err := r.reviews.Settle(ctx, row.ID, v.approved, r.reviewer, v.answer); err != nil {
			r.say("decision: settle %d: %v", row.ID, err)
			continue
		}
		settled++
	}
	if settled > 0 {
		if left, err := r.reviews.Pending(ctx); err == nil && len(left) > 0 {
			r.dirty.Store(true) // the rest stay pending for the next turn end
		}
	}
	return nil
}

func (r *Reviewer) say(format string, args ...any) {
	if r.loud != nil {
		r.loud(fmt.Sprintf(format, args...))
	}
}

const reviewContract = "Review these recorded decisions. For each row, judge the answer against the question and the state. Reply with one verdict line per row, as the last lines of your reply: `verdict: <id> approve` when the answer is right, or `verdict: <id> deny <corrected answer>` when it is wrong (a deny needs the corrected answer). Name every row; a row you do not name stays pending.\n"

func reviewPrompt(rows []ReviewRow) string {
	var b strings.Builder
	b.WriteString(reviewContract)
	for _, row := range rows {
		b.WriteString(rowBlock(row))
	}
	return b.String()
}

func rowBlock(row ReviewRow) string {
	var b strings.Builder
	b.WriteString("\n== " + strconv.FormatInt(row.ID, 10) + " · " + row.Site + "\n")
	b.WriteString("question: " + row.Question.Prompt)
	if row.Question.Kind == KindChoice && len(row.Question.Choices) > 0 {
		b.WriteString(" (choice: " + strings.Join(row.Question.Choices, ", ") + ")")
	}
	b.WriteString("\n")
	conf := ""
	if row.Confidence != nil {
		conf = fmt.Sprintf(" (confidence %.2f by %s)", *row.Confidence, row.Decider)
	}
	b.WriteString("answer: " + row.Answer + conf + "\n")
	if row.State != "" {
		b.WriteString("state: " + row.State + "\n")
	}
	return b.String()
}

// fitRows takes the oldest rows whose review fits the fire's prompt budget
// in tokens, at the codebase's own four bytes to the token; the rest stay
// pending for the next turn end. A row that cannot fit alone still goes,
// so one huge row cannot wedge the queue.
func fitRows(rows []ReviewRow, header string, budget int) []ReviewRow {
	used := tokens(header)
	take := 0
	for i, row := range rows {
		used += tokens(rowBlock(row))
		if i > 0 && used > budget {
			break
		}
		take = i + 1
	}
	return rows[:take]
}

func tokens(s string) int { return (len(s) + 3) / 4 }

type verdict struct {
	approved bool
	answer   string
}

func parseVerdicts(out string) map[int64]verdict {
	verdicts := map[int64]verdict{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "verdict:")
		if !ok {
			continue
		}
		fields := strings.SplitN(strings.TrimSpace(rest), " ", 3)
		if len(fields) < 2 {
			continue
		}
		id, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch fields[1] {
		case "approve":
			verdicts[id] = verdict{approved: true}
		case "deny":
			answer := ""
			if len(fields) == 3 {
				answer = strings.TrimSpace(fields[2])
			}
			if len(answer) > maxCorrection {
				answer = answer[:maxCorrection]
			}
			verdicts[id] = verdict{approved: false, answer: answer}
		}
	}
	return verdicts
}
