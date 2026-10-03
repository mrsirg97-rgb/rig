package command

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type decideCmd struct{}

func (decideCmd) Sub() []Sub {
	return []Sub{
		{Name: "review", Desc: "drain the pending decision rows now (a no-tools worker judges and settles them; the outcome arrives as a notice)"},
	}
}

func (decideCmd) Name() string { return "decide" }

func (decideCmd) Description() string {
	return "decision reviews: bare shows the pending count, review drains the pending rows now"
}

func (decideCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(args)
	switch {
	case len(fields) == 0:
		if e.PendingReviews == nil {
			return "", errors.New("decide: no reviewer (headless workers and jobs never review)")
		}
		n, err := e.PendingReviews(ctx)
		if err != nil {
			return "", err
		}
		if n == 0 {
			return "decide: no pending rows", nil
		}
		return fmt.Sprintf("decide: %d pending rows; /decide review drains them now", n), nil
	case len(fields) == 1 && fields[0] == "review":
		if e.Review == nil {
			return "", errors.New("decide: no reviewer (headless workers and jobs never review)")
		}
		return e.Review(ctx)
	default:
		return "", errors.New("decide: usage: decide [review]")
	}
}
