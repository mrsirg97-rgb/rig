package decision

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type PTrue struct {
	GoldTrue  float64 `json:"goldTrue"`
	GoldFalse float64 `json:"goldFalse"`
}

type QuestionReport struct {
	N        int     `json:"n"`
	Correct  int     `json:"correct"`
	Accuracy float64 `json:"accuracy"`
	PTrue    *PTrue  `json:"pTrue,omitempty"`
}

type Report struct {
	Checkpoint string                    `json:"checkpoint,omitempty"`
	Questions  map[string]QuestionReport `json:"questions"`
}

func ParseReport(raw string) (Report, error) {
	var out Report
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return Report{}, fmt.Errorf("decision: the report is not JSON: %v", err)
	}
	if len(out.Questions) == 0 {
		return Report{}, fmt.Errorf("decision: the report carries no questions")
	}
	for id, q := range out.Questions {
		if q.N <= 0 {
			return Report{}, fmt.Errorf("decision: question %s reports n %d", id, q.N)
		}
		if q.Accuracy < 0 || q.Accuracy > 1 {
			return Report{}, fmt.Errorf("decision: question %s reports accuracy %g", id, q.Accuracy)
		}
		if q.PTrue != nil && (q.PTrue.GoldTrue < 0 || q.PTrue.GoldTrue > 1 || q.PTrue.GoldFalse < 0 || q.PTrue.GoldFalse > 1) {
			return Report{}, fmt.Errorf("decision: question %s reports p(true) outside the probability range", id)
		}
	}
	return out, nil
}

type GoldRow struct {
	ID        int64
	State     string
	Question  Question
	Answer    string
	Corrected *string
	Status    string
}

var labelRe = regexp.MustCompile(`(?i)(?:answer|label|class|rating)(?: is)?:?\s*` + "`?\"?" + `(yes|no|safe|changes|dangerous)\b|^\W*(yes|no|safe|changes|dangerous)\b|(?:right|correct) (?:label|class|rating|answer) is\s*` + "`?\"?" + `(yes|no|safe|changes|dangerous)`)

func ParseReviewerLabel(s string) (string, bool) {
	m := labelRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", false
	}
	for _, g := range m[1:] {
		if g != "" {
			return strings.ToLower(g), true
		}
	}
	return "", false
}

type LayaQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

type LayaRow struct {
	ID        int64                   `json:"id"`
	State     string                  `json:"state"`
	Questions map[string]LayaQuestion `json:"questions"`
	Expected  map[string]any          `json:"expected"`
}

type GoldSet struct {
	Rows    []LayaRow
	Skipped int
}

func GoldRows(settled []GoldRow) (GoldSet, error) {
	out := GoldSet{Rows: make([]LayaRow, 0, len(settled))}
	for _, r := range settled {
		if r.Question.ID == "" {
			return GoldSet{}, fmt.Errorf("decision: gold row %d carries no question id", r.ID)
		}
		row, ok, err := goldRow(r)
		if err != nil {
			return GoldSet{}, err
		}
		if !ok {
			out.Skipped++
			continue
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

func goldRow(r GoldRow) (LayaRow, bool, error) {
	switch r.Question.Kind {
	case KindScore:
		return LayaRow{}, false, nil
	case KindChoice, KindBinary:
	default:
		return LayaRow{}, false, nil
	}
	gold := r.Answer
	if r.Status != StatusApproved {
		if r.Corrected == nil {
			return LayaRow{}, false, nil
		}
		label, ok := ParseReviewerLabel(*r.Corrected)
		if !ok {
			return LayaRow{}, false, nil
		}
		gold = label
	}
	q := LayaQuestion{Instructions: r.Question.Prompt}
	expected := any(gold)
	switch r.Question.Kind {
	case KindBinary:
		if gold != "yes" && gold != "no" {
			return LayaRow{}, false, nil
		}
		q.Type = wireNoul
		expected = gold == "yes"
	case KindChoice:
		known := false
		for _, c := range r.Question.Choices {
			if c == gold {
				known = true
				break
			}
		}
		if !known {
			return LayaRow{}, false, nil
		}
		q.Type = KindChoice
		q.Criteria = described(r.Question)
	}
	return LayaRow{
		ID:        r.ID,
		State:     r.State,
		Questions: map[string]LayaQuestion{r.Question.ID: q},
		Expected:  map[string]any{r.Question.ID: expected},
	}, true, nil
}

func Split(rows []LayaRow) (train, held []LayaRow) {
	groups := make(map[string][]LayaRow)
	order := make([]string, 0, len(rows))
	for _, r := range rows {
		k := groupKey(r)
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	train = make([]LayaRow, 0, len(rows))
	for _, k := range order {
		g := groups[k]
		sort.Slice(g, func(i, j int) bool { return g[i].ID < g[j].ID })
		for i, r := range g {
			if i%5 == 0 {
				held = append(held, r)
			} else {
				train = append(train, r)
			}
		}
	}
	return train, held
}

func groupKey(r LayaRow) string {
	ids := make([]string, 0, len(r.Questions))
	for id := range r.Questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, id+"="+fmt.Sprintf("%v", r.Expected[id]))
	}
	return strings.Join(parts, "\x00")
}

func ConstantReport(train, held []LayaRow) Report {
	out := Report{Questions: map[string]QuestionReport{}}
	for id := range questionTypes(train, held) {
		trainGold, trainType := goldCounts(train, id)
		heldGold, _ := goldCounts(held, id)
		if len(heldGold) == 0 || len(trainGold) == 0 {
			continue
		}
		majority, best := "", 0
		for label, n := range trainGold {
			if n > best || (n == best && label < majority) {
				majority, best = label, n
			}
		}
		qr := QuestionReport{N: lenOf(heldGold), Correct: heldGold[majority], Accuracy: float64(heldGold[majority]) / float64(lenOf(heldGold))}
		if trainType == wireNoul {
			yes := trainGold["true"]
			qr.PTrue = &PTrue{GoldTrue: float64(yes) / float64(lenOf(trainGold)), GoldFalse: float64(yes) / float64(lenOf(trainGold))}
		}
		out.Questions[id] = qr
	}
	return out
}

func questionTypes(rowSets ...[]LayaRow) map[string]string {
	out := map[string]string{}
	for _, rows := range rowSets {
		for _, r := range rows {
			for id, q := range r.Questions {
				out[id] = q.Type
			}
		}
	}
	return out
}

func goldCounts(rows []LayaRow, id string) (map[string]int, string) {
	counts := map[string]int{}
	kind := ""
	for _, r := range rows {
		q, ok := r.Questions[id]
		if !ok {
			continue
		}
		kind = q.Type
		counts[fmt.Sprintf("%v", r.Expected[id])]++
	}
	return counts, kind
}

func lenOf(counts map[string]int) int {
	n := 0
	for _, c := range counts {
		n += c
	}
	return n
}

func Beats(candidate, constant, incumbent Report) bool {
	ids := make(map[string]bool)
	for id := range candidate.Questions {
		ids[id] = true
	}
	for id := range constant.Questions {
		ids[id] = true
	}
	for id := range incumbent.Questions {
		ids[id] = true
	}
	if len(ids) == 0 {
		return false
	}
	for id := range ids {
		c, okc := candidate.Questions[id]
		k, okk := constant.Questions[id]
		i, oki := incumbent.Questions[id]
		if !okc || !okk || !oki {
			return false
		}
		if c.Accuracy <= k.Accuracy || c.Accuracy <= i.Accuracy {
			return false
		}
	}
	return true
}
