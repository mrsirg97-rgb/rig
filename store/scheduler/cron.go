package scheduler

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type fieldSpec struct {
	name string
	min  int
	max  int
}

var cronFields = []fieldSpec{
	{name: "minute", min: 0, max: 59},
	{name: "hour", min: 0, max: 23},
	{name: "day-of-month", min: 1, max: 31},
	{name: "month", min: 1, max: 12},
	{name: "day-of-week", min: 0, max: 7},
}

var invalidCharRe = regexp.MustCompile(`^[0-9*,/-]+$`)

type CronSet struct {
	Values []int
	IsStar bool
}

type ParsedCron struct {
	Minute CronSet
	Hour   CronSet
	Dom    CronSet
	Month  CronSet
	Dow    CronSet
}

func cronFail(format string, a ...any) error {
	return fmt.Errorf("cron: "+format, a...)
}

func ValidateCron(expr string) (ParsedCron, error) {
	text := strings.TrimSpace(expr)
	if strings.HasPrefix(text, "@") {
		return ParsedCron{}, cronFail("@-macros are not supported; use 5 fields")
	}
	tokens := strings.Fields(text)
	if len(tokens) != 5 {
		return ParsedCron{}, cronFail("expected 5 fields (minute hour dom month dow), got %d", len(tokens))
	}
	var out ParsedCron
	keys := [5]*CronSet{&out.Minute, &out.Hour, &out.Dom, &out.Month, &out.Dow}
	for i, token := range tokens {
		if !invalidCharRe.MatchString(token) {
			return ParsedCron{}, cronFail("field '%s' has an invalid character in '%s'", cronFields[i].name, token)
		}
		set, err := parseField(token, cronFields[i])
		if err != nil {
			return ParsedCron{}, err
		}
		*keys[i] = set
	}
	return out, nil
}

func parseField(token string, spec fieldSpec) (CronSet, error) {
	isStar := strings.HasPrefix(token, "*")
	values := map[int]bool{}
	for _, item := range strings.Split(token, ",") {
		if item == "" {
			return CronSet{}, cronFail("field '%s' has an empty list item in '%s'", spec.name, token)
		}
		vs, err := parseItem(item, spec, token)
		if err != nil {
			return CronSet{}, err
		}
		for _, v := range vs {
			values[v] = true
		}
	}
	list := make([]int, 0, len(values))
	for v := range values {
		list = append(list, v)
	}
	sort.Ints(list)
	if spec.name == "day-of-week" {
		for i, v := range list {
			if v == 7 {
				list[i] = 0
			}
		}
		sort.Ints(list)
		list = dedupe(list)
	}
	return CronSet{Values: list, IsStar: isStar}, nil
}

func dedupe(vs []int) []int {
	out := vs[:0]
	var last int
	for i, v := range vs {
		if i > 0 && v == last {
			continue
		}
		out = append(out, v)
		last = v
	}
	return out
}

var itemRe = regexp.MustCompile(`^(\*|\d+(?:-\d+)?)(?:/(\d+))?$`)

func parseItem(item string, spec fieldSpec, token string) ([]int, error) {
	m := itemRe.FindStringSubmatch(item)
	if m == nil {
		return nil, cronFail("field '%s' has an invalid item '%s' in '%s'", spec.name, item, token)
	}
	base := m[1]
	step := 1
	if m[2] != "" {
		fmt.Sscanf(m[2], "%d", &step)
	}
	if step < 1 {
		return nil, cronFail("field '%s' step %d in '%s' must be >= 1", spec.name, step, item)
	}
	var lo, hi int
	switch {
	case base == "*":
		lo, hi = spec.min, spec.max
	case strings.Contains(base, "-"):
		parts := strings.SplitN(base, "-", 2)
		fmt.Sscanf(parts[0], "%d", &lo)
		fmt.Sscanf(parts[1], "%d", &hi)
		if lo > hi {
			return nil, cronFail("field '%s' range %s in '%s' must not wrap", spec.name, base, item)
		}
	default:
		if m[2] != "" {
			return nil, cronFail("field '%s' item '%s': step requires * or a range", spec.name, item)
		}
		fmt.Sscanf(base, "%d", &lo)
		hi = lo
	}
	if lo < spec.min || hi > spec.max {
		return nil, cronFail("field '%s' value in '%s' out of range %d-%d", spec.name, item, spec.min, spec.max)
	}
	out := []int{}
	for v := lo; v <= hi; v += step {
		out = append(out, v)
	}
	return out, nil
}

const dayCap = 1462

func NextFire(parsed ParsedCron, from time.Time) (time.Time, bool) {
	first := from.Truncate(time.Minute).Add(time.Minute)

	floorH := first.Hour()
	floorM := first.Minute()

	for day := 0; day <= dayCap; day++ {
		d := first.AddDate(0, 0, day)
		if !inSet(parsed.Month.Values, int(d.Month())) {
			continue
		}
		if !dayMatches(parsed, d) {
			continue
		}
		for _, h := range parsed.Hour.Values {
			if day == 0 && h < floorH {
				continue
			}
			for _, mi := range parsed.Minute.Values {
				if day == 0 && h == floorH && mi < floorM {
					continue
				}
				return time.Date(d.Year(), d.Month(), d.Day(), h, mi, 0, 0, from.Location()), true
			}
		}
	}
	return time.Time{}, false
}

func inSet(vs []int, v int) bool {
	for _, x := range vs {
		if x == v {
			return true
		}
	}
	return false
}

func dayMatches(parsed ParsedCron, d time.Time) bool {
	domHit := inSet(parsed.Dom.Values, d.Day())
	dowHit := inSet(parsed.Dow.Values, int(d.Weekday()))
	domRestricted := !parsed.Dom.IsStar
	dowRestricted := !parsed.Dow.IsStar
	switch {
	case domRestricted && dowRestricted:
		return domHit || dowHit
	case domRestricted:
		return domHit
	case dowRestricted:
		return dowHit
	}
	return true
}
