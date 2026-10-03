package command

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type remCmd struct{}

func (remCmd) Sub() []Sub {
	return []Sub{
		{Name: "list", Desc: "show the memories that fit the screen: list [all|<n>]"},
		{Name: "show", Desc: "print one memory in full: show <id>"},
		{Name: "forget", Desc: "delete a memory: forget <id>"},
		{Name: "project", Desc: "show another project's memories: project <path> [all|<n>]"},
	}
}

func (remCmd) Name() string { return "rem" }

func (remCmd) Description() string {
	return "the memories: list them, print one, forget one, or read another project's"
}

func (remCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(args)
	switch {
	case len(fields) == 0 || (fields[0] == "list" && len(fields) <= 2):
		if e.RemList == nil {
			return "", errors.New("rem: no rem seam (the root did not wire one)")
		}
		var tail []string
		if len(fields) == 2 {
			tail = fields[1:]
		}
		limit, err := listLimit(e, tail)
		if err != nil {
			return "", fmt.Errorf("rem: list: %v", err)
		}
		rows, err := e.RemList(ctx, "")
		if err != nil {
			return "", err
		}
		return renderRemList(rows, limit, "rem list all"), nil
	case fields[0] == "project" && (len(fields) == 2 || len(fields) == 3):
		if e.RemList == nil {
			return "", errors.New("rem: no rem seam (the root did not wire one)")
		}
		limit, err := listLimit(e, fields[2:])
		if err != nil {
			return "", fmt.Errorf("rem: project: %v", err)
		}
		rows, err := e.RemList(ctx, fields[1])
		if err != nil {
			return "", err
		}
		if len(rows) == 0 {
			if e.RemLabel == nil {
				return "", errors.New("rem: no rem seam (the root did not wire one)")
			}
			label, err := e.RemLabel(ctx, fields[1])
			if err != nil {
				return "", err
			}
			return "rem: no memories in " + label, nil
		}
		return renderRemList(rows, limit, "rem project "+fields[1]+" all"), nil
	case fields[0] == "show" && len(fields) == 2:
		id, err := remID(fields[1])
		if err != nil {
			return "", err
		}
		if e.RemShow == nil {
			return "", errors.New("rem: no rem seam (the root did not wire one)")
		}
		row, err := e.RemShow(ctx, id)
		if err != nil {
			return "", err
		}
		return renderRemShow(row), nil
	case fields[0] == "forget" && len(fields) == 2:
		id, err := remID(fields[1])
		if err != nil {
			return "", err
		}
		if e.RemForget == nil {
			return "", errors.New("rem: no rem seam (the root did not wire one)")
		}
		if err := e.RemForget(ctx, id); err != nil {
			return "", err
		}
		return fmt.Sprintf("rem: forgot m%d", id), nil
	}
	switch {
	case len(fields) > 0 && fields[0] == "show":
		if len(fields) == 1 {
			return "", errors.New("rem: show needs an id (rem show <id>)")
		}
		return "", errors.New("rem: show takes one id")
	case len(fields) > 0 && fields[0] == "forget":
		if len(fields) == 1 {
			return "", errors.New("rem: forget needs an id (rem forget <id>)")
		}
		return "", errors.New("rem: forget takes one id")
	case len(fields) > 0 && fields[0] == "project":
		if len(fields) == 1 {
			return "", errors.New("rem: project takes a path (rem project <path> [all|<n>])")
		}
		return "", errors.New("rem: project takes one path and at most a count (rem project <path> [all|<n>])")
	case len(fields) > 0 && fields[0] == "list":
		return "", errors.New("rem: list takes at most a count (rem list [all|<n>])")
	default:
		return "", errors.New("rem: usage: rem [list [all|<n>]|show <id>|forget <id>|project <path> [all|<n>]]")
	}
}

func remID(s string) (int64, error) {
	s = strings.TrimPrefix(s, "m")
	if s == "" {
		return 0, errors.New("rem: the id must be a memory id (m<N> or <N>)")
	}
	var id int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("rem: the id must be a memory id (m<N> or <N>)")
		}
	}
	if _, err := fmt.Sscanf(s, "%d", &id); err != nil || id < 1 {
		return 0, errors.New("rem: the id must be a memory id (m<N> or <N>)")
	}
	return id, nil
}

func renderRemList(rows []RemRow, limit int, verb string) string {
	if len(rows) == 0 {
		return "rem: no memories"
	}
	shown, hidden := fit(len(rows), limit)
	var b strings.Builder
	b.WriteString(plural(len(rows), "memory"))
	for _, r := range rows[:shown] {
		b.WriteString("\n" + row(fmt.Sprintf("m%d", r.ID), 0, "", r.Kind,
			ageOf(r.CreatedAt), fmt.Sprintf("%.2f", r.Strength), firstRunes(r.Content, 80)))
	}
	b.WriteString(moreFooter(hidden, verb))
	return b.String()
}

func renderRemShow(r RemRow) string {
	head := fmt.Sprintf("m%d [%.2f] %s · %s", r.ID, r.Strength, r.ScopeLabel, r.Kind)
	if r.Superseded != nil {
		head += fmt.Sprintf(" · superseded by m%d", *r.Superseded)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", head)
	fmt.Fprintf(&b, "created %s · strength %.2f · importance %.2f\n", ageOf(r.CreatedAt), r.Strength, r.Importance)
	if r.Source != "" {
		fmt.Fprintf(&b, "source: %s\n", r.Source)
	}
	fmt.Fprintf(&b, "content:\n%s", indent(r.Content))
	return b.String()
}

func ageOf(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < 0:
		return "?"
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}

func firstRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}

func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		fmt.Fprintf(&b, "  %s\n", line)
	}
	return b.String()
}
