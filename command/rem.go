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
		{Name: "list", Desc: "show the live memories"},
		{Name: "show", Desc: "show a memory: show <id>"},
		{Name: "forget", Desc: "forget a memory: forget <id>"},
	}
}

func (remCmd) Name() string { return "rem" }

func (remCmd) Description() string {
	return "list, show, or forget the live memories"
}

func (remCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(args)
	switch {
	case len(fields) == 0 || (len(fields) == 1 && fields[0] == "list"):
		if e.RemList == nil {
			return "", errors.New("rem: no rem seam (the root did not wire one)")
		}
		rows, err := e.RemList(ctx)
		if err != nil {
			return "", err
		}
		return renderRemList(rows), nil
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
	default:
		return "", errors.New("rem: usage: rem [list|show|forget <id>]")
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

func renderRemList(rows []RemRow) string {
	if len(rows) == 0 {
		return "rem: no memories"
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "m%d · %s · %s · %.2f · %s\n",
			r.ID, r.Kind, ageOf(r.CreatedAt), r.Strength, firstRunes(r.Content, 80))
	}
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
