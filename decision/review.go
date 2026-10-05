package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/mrsirg97-rgb/rig/v2"
	"github.com/mrsirg97-rgb/rig/v2/broadcast"
	"github.com/mrsirg97-rgb/rig/v2/core"
	"github.com/mrsirg97-rgb/rig/v2/evt"
	"github.com/mrsirg97-rgb/rig/v2/models"
	"github.com/mrsirg97-rgb/rig/v2/store/scope"
)

const maxCorrection = 1024

type ReviewRow struct {
	ID         int64
	Scope      string
	Site       string
	State      string
	Question   Question
	Answer     string
	Confidence *float64
	Decider    string
}

type Reviews interface {
	Settled
	Pending(ctx context.Context) ([]ReviewRow, error)
	Settle(ctx context.Context, id int64, approved bool, reviewer, answer string) error
}

type Fire func(ctx context.Context, prompt string, voice broadcast.Member) (model string, err error)

type Reviewer struct {
	ctx      context.Context
	engine   evt.Engine
	dirty    atomic.Bool
	posted   atomic.Bool
	reviews  Reviews
	fire     Fire
	batch    int
	row      models.Model
	scope    string
	room     broadcast.Room
	self     broadcast.Member
	verdicts map[int64]core.Verdict
	speaking atomic.Int64
}

func NewReviewer(ctx context.Context, engine evt.Engine, reviews Reviews, fire Fire, batch int, row models.Model, room broadcast.Room, scope string) *Reviewer {
	if engine == nil || room == nil {
		panic("decision: the reviewer bites on the loop and hears verdicts in a room; both are constructor arguments")
	}
	r := &Reviewer{
		ctx:      ctx,
		engine:   engine,
		reviews:  reviews,
		fire:     fire,
		batch:    batch,
		row:      row,
		scope:    scope,
		room:     room,
		self:     room.Add(rig.MemberDecision),
		verdicts: map[int64]core.Verdict{},
	}
	r.self.Subscribe(ctx, r.receive)
	return r
}

func (r *Reviewer) receive(err error, messages ...broadcast.Message) {
	if err != nil {
		return
	}
	for _, m := range messages {
		switch ev := m.Event().(type) {
		case core.Verdict:
			r.verdicts[ev.Row] = ev
		case core.ReasoningDelta:
			if m.Origin() == r.speaking.Load() {
				r.phase(core.Phase{Name: phaseReviewing, Text: ev.Text})
			}
		}
	}
}

const phaseReviewing = "reviewing"

func (r *Reviewer) phase(p core.Phase) {
	p.Name = phaseReviewing
	r.self.Publish(context.Background(), func(error) {}, broadcast.NewMessage(r.self.Id(), true, p))
}

func (r *Reviewer) speak(ctx context.Context, rows []ReviewRow) (string, error) {
	voice := r.room.Mint()
	r.speaking.Store(voice.Id())
	r.phase(core.Phase{})
	reviewer, err := r.fire(ctx, reviewPrompt(rows), voice)
	voice.Leave()
	return reviewer, err
}

func (r *Reviewer) settled(n int, err error) {
	r.speaking.Store(0)
	if err != nil {
		r.phase(core.Phase{Done: true, Note: err.Error()})
		return
	}
	r.phase(core.Phase{Done: true, Ok: true, Note: fmt.Sprintf("%d %s settled", n, plural(n, "row"))})
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func (r *Reviewer) call(fn func()) {
	done := make(chan struct{})
	if r.engine.Add(evt.Func(func(context.Context) { fn(); close(done) }), rig.PriorityReview) == 0 {
		fn()
		return
	}
	<-done
}

func (r *Reviewer) Land() { r.dirty.Store(true) }

func (r *Reviewer) Wake() {
	if !r.dirty.Load() {
		return
	}
	if r.posted.Swap(true) {
		return
	}
	r.engine.Add(evt.Func(func(context.Context) {
		r.posted.Store(false)
		r.dirty.Store(false)
		r.bite()
	}), rig.PriorityReview)
}

func (r *Reviewer) bite() {
	rows, all, _, err := r.take(r.ctx)
	if err != nil {
		r.say("review: %v", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	go func() {
		reviewer, err := r.speak(r.ctx, rows)
		r.engine.Add(evt.Func(func(context.Context) {
			if err != nil {
				r.say("review: fire: %v", err)
				r.settled(0, err)
				return
			}
			r.settled(r.settle(r.ctx, rows, all, reviewer), nil)
		}), rig.PriorityReview)
	}()
}

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

func (r *Reviewer) Drain(ctx context.Context) (string, error) {
	rows, all, waiting, err := r.take(ctx)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		if r.batch == 0 {
			return "reviewBatch is 0: the reviewer stays off", nil
		}
		if waiting > 0 {
			return fmt.Sprintf("nothing pending here; %d rows wait for their project", waiting), nil
		}
		return "nothing pending", nil
	}
	reviewer, err := r.speak(ctx, rows)
	if err != nil {
		r.settled(0, err)
		return "", fmt.Errorf("fire: %w", err)
	}
	settled := 0
	r.call(func() { settled = r.settle(ctx, rows, all, reviewer) })
	r.settled(settled, nil)
	report := fmt.Sprintf("fired %d rows, settled %d, %d stay pending", len(rows), settled, len(all)-len(rows))
	if waiting > 0 {
		report += fmt.Sprintf(", %d wait for their project", waiting)
	}
	return report, nil
}

func (r *Reviewer) take(ctx context.Context) (rows, all []ReviewRow, waiting int, err error) {
	if r.batch < 0 {
		return nil, nil, 0, fmt.Errorf("reviewBatch %d: a negative batch is refused", r.batch)
	}
	if r.batch == 0 {
		return nil, nil, 0, nil
	}
	pending, err := r.reviews.Pending(ctx)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("pending: %w", err)
	}
	for _, row := range pending {
		if r.sees(row) {
			all = append(all, row)
		} else {
			waiting++
		}
	}
	if len(all) == 0 {
		return nil, nil, waiting, nil
	}
	cost := VerdictCost()
	rows = fitRows(all, reviewContract, r.row.Window-r.row.Reserve)
	if reply := r.row.MaxTokens / cost; reply < 1 {
		rows = rows[:1]
	} else if reply < len(rows) {
		rows = rows[:reply]
	}
	if len(rows) > r.batch {
		rows = rows[:r.batch]
	}
	return rows, all, waiting, nil
}

func (r *Reviewer) sees(row ReviewRow) bool {
	return row.Scope == r.scope || row.Scope == scope.Global
}

func (r *Reviewer) settle(ctx context.Context, rows, all []ReviewRow, reviewer string) int {
	verdicts := r.verdicts
	r.verdicts = map[int64]core.Verdict{}
	answered := answeredRows(rows, verdicts)
	if len(answered) > 0 && len(rows) < len(all) {
		r.dirty.Store(true)
	}
	settled := 0
	for _, row := range answered {
		v := verdicts[row.ID]
		answer := v.Reason
		if len(answer) > maxCorrection {
			answer = answer[:maxCorrection]
		}
		if err := r.reviews.Settle(ctx, row.ID, v.Accept, reviewer, answer); err != nil {
			r.say("settle %d: %v", row.ID, err)
			continue
		}
		settled++
	}
	return settled
}

func (r *Reviewer) say(format string, args ...any) {
	broadcast.Say(r.self, "decision", fmt.Sprintf(format, args...), core.LevelError)
}

const reviewContract = "Review these recorded decisions. For each row, judge the answer against the question and the state, then call the verdict tool naming the row: accept when the answer is right, reject with the corrected answer as the reason when it is wrong. When a row is about code, look before you judge: pack the symbol with rem or read the file the state names, and say in the reason what you read; a verdict on code you did not look at is a guess. Name every row; a row you do not name stays pending.\n"

func VerdictCost() int {
	call, _ := json.Marshal(map[string]any{"row": int64(math.MaxInt64), "accept": false, "reason": strings.Repeat("v", maxCorrection)})
	return tokens("verdict" + string(call))
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

func answeredRows(rows []ReviewRow, verdicts map[int64]core.Verdict) []ReviewRow {
	var out []ReviewRow
	for _, row := range rows {
		v, ok := verdicts[row.ID]
		if !ok || (!v.Accept && v.Reason == "") {
			continue
		}
		out = append(out, row)
	}
	return out
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
