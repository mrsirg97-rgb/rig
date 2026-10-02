package decision

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
	reviews  Reviews
	fire     Fire
	reviewer string
	loud     func(string)
}

func NewReviewer(reviews Reviews, fire Fire, reviewer string, loud func(string)) *Reviewer {
	return &Reviewer{
		wake:     make(chan struct{}, 1),
		reviews:  reviews,
		fire:     fire,
		reviewer: reviewer,
		loud:     loud,
	}
}

func (r *Reviewer) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Reviewer) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
			if err := r.Drain(ctx); err != nil {
				r.say("decision: review: %v", err)
			}
		}
	}
}

func (r *Reviewer) Drain(ctx context.Context) error {
	rows, err := r.reviews.Pending(ctx)
	if err != nil {
		return fmt.Errorf("pending: %w", err)
	}
	if len(rows) == 0 {
		return nil
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
			r.Wake()
		}
	}
	return nil
}

func (r *Reviewer) say(format string, args ...any) {
	if r.loud != nil {
		r.loud(fmt.Sprintf(format, args...))
	}
}

func reviewPrompt(rows []ReviewRow) string {
	var b strings.Builder
	b.WriteString("Review these recorded decisions. For each row, judge the answer against the question and the state. Reply with one verdict line per row, as the last lines of your reply: `verdict: <id> approve` when the answer is right, or `verdict: <id> deny <corrected answer>` when it is wrong (a deny needs the corrected answer). Name every row; a row you do not name stays pending.\n")
	for _, row := range rows {
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
	}
	return b.String()
}

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
