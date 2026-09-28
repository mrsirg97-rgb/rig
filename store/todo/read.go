package todo

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/store"
	"strings"
)

func Read(ctx context.Context, db store.DB, p Project, session string) (string, error) {
	return read(ctx, db, p, session, modePresent, 0)
}

func ReadAll(ctx context.Context, db store.DB, p Project, session string) (string, error) {
	return read(ctx, db, p, session, modeAll, 0)
}

func ReadFinished(ctx context.Context, db store.DB, p Project, session string, n int) (string, error) {
	if n <= 0 {
		n = DefaultFinishedShown
	}
	if n < 1 || n > FinishedListCap {
		return "", fmt.Errorf("todo: finished count must be an integer 1-%d, got %d", FinishedListCap, n)
	}
	return read(ctx, db, p, session, modeFinished, n)
}

func read(ctx context.Context, db store.DB, p Project, session string, mode readMode, n int) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		return replyText(f, session, "", mode, n, p.Label), nil
	})
}

func ReadOne(ctx context.Context, db store.DB, p Project, id, session string) (string, error) {
	if session == "" {
		session = anon
	}
	return mutate(ctx, db, p, func(bound context.Context, tx *sql.Tx, f *folded) (string, error) {
		if e := rewrite(tx, f, p.Key); e != nil {
			return "", e
		}
		ts, ok := f.tasks[id]
		if !ok {
			return "", unknownTask(p, id)
		}
		var b strings.Builder
		b.WriteString(renderOne(f, ts, session))
		b.WriteString("\n" + summaryOf(f, defaultShown(f)))
		if foot := staleFooter(f); foot != "" {
			b.WriteString("\n" + foot)
		}
		return b.String(), nil
	})
}

func Notes(ctx context.Context, db store.DB, p Project, id, session string) (string, error) {
	if session == "" {
		session = anon
	}
	_, tx, err := db.TxReadOnly(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx, p.Key)
	if err != nil {
		return "", err
	}
	f.label, f.notRepo = p.Label, p.OutsideRepo
	ts, ok := f.tasks[id]
	if !ok {
		return "", unknownTask(p, id)
	}
	if len(ts.notes) == 0 {
		return "no notes on " + id, nil
	}
	var b strings.Builder
	b.WriteString(lineOf(f, ts, session))
	for _, n := range ts.notes {
		fmt.Fprintf(&b, "\n\u00b7 %s (by %s, %s)", n.text, n.session, n.ts)
	}
	return b.String(), nil
}
