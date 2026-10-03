package decision

import (
	"context"
	"errors"
	"fmt"
	"math"
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

type Fire func(ctx context.Context, prompt string) (reply, model string, err error)

type Reviewer struct {
	reviews Reviews
	fire    Fire
	budget  int
	maxOut  int
	loud    func(string)
}

func NewReviewer(reviews Reviews, fire Fire, budget, maxOut int, loud func(string)) *Reviewer {
	return &Reviewer{
		reviews: reviews,
		fire:    fire,
		budget:  budget,
		maxOut:  maxOut,
		loud:    loud,
	}
}

func (r *Reviewer) Drain(ctx context.Context) (string, error) {
	all, err := r.reviews.Pending(ctx)
	if err != nil {
		return "", fmt.Errorf("pending: %w", err)
	}
	if len(all) == 0 {
		return "nothing pending", nil
	}
	cost := VerdictLineCost()
	if cost <= 0 {
		return "", errors.New("the contract lost its verdict lines; the reply bound cannot be derived")
	}
	rows := fitRows(all, reviewContract, r.budget)
	if reply := r.maxOut / cost; reply < 1 {
		rows = rows[:1]
	} else if reply < len(rows) {
		rows = rows[:reply]
	}
	out, reviewer, err := r.fire(ctx, reviewPrompt(rows))
	if err != nil {
		return "", fmt.Errorf("fire: %w", err)
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
		if err := r.reviews.Settle(ctx, row.ID, v.approved, reviewer, v.answer); err != nil {
			r.say("settle %d: %v", row.ID, err)
			continue
		}
		settled++
	}
	return fmt.Sprintf("fired %d rows, settled %d, %d stay pending", len(rows), settled, len(all)-len(rows)), nil
}

func (r *Reviewer) say(format string, args ...any) {
	if r.loud != nil {
		r.loud(fmt.Sprintf(format, args...))
	}
}

const reviewContract = "Review these recorded decisions. For each row, judge the answer against the question and the state. Reply with one verdict line per row, as the last lines of your reply: `verdict: <id> approve` when the answer is right, or `verdict: <id> deny <corrected answer>` when it is wrong (a deny needs the corrected answer). Name every row; a row you do not name stays pending.\n"

func VerdictLineCost() int {
	cost := 0
	for _, seg := range strings.Split(reviewContract, "`") {
		if !strings.HasPrefix(seg, "verdict: ") {
			continue
		}
		line := strings.ReplaceAll(seg, "<id>", strconv.FormatInt(math.MaxInt64, 10))
		line = strings.ReplaceAll(line, "<corrected answer>", strings.Repeat("v", maxCorrection))
		if c := tokens(line); c > cost {
			cost = c
		}
	}
	return cost
}

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
