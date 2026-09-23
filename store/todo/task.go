package todo

import (
	"context"

	"github.com/mrsirg97-rgb/rig/store"
)

// TaskInfo is the structured read the swarm's brief needs: the task's own
// text and its notes, each with the session that wrote it, in order. The
// rendered Read reply is the model's surface, not a parser contract, so the
// brief does not parse words.
type TaskInfo struct {
	ID    string
	Text  string
	Notes []TaskNote
}

type TaskNote struct {
	Text    string
	Session string
}

// Task returns one task's brief: its text and notes, read-only. The
// unknown id is refused in the store's voice.
func Task(ctx context.Context, db store.DB, p Project, id, session string) (TaskInfo, error) {
	if session == "" {
		session = anon
	}
	_, tx, err := db.TxReadOnly(ctx)
	if err != nil {
		return TaskInfo{}, err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx, p.Key)
	if err != nil {
		return TaskInfo{}, err
	}
	f.label, f.notRepo = p.Label, p.OutsideRepo
	ts, ok := f.tasks[id]
	if !ok {
		return TaskInfo{}, unknownTask(p, id)
	}
	notes := make([]TaskNote, len(ts.notes))
	for i, n := range ts.notes {
		notes[i] = TaskNote{Text: n.text, Session: n.session}
	}
	return TaskInfo{ID: ts.id, Text: ts.text, Notes: notes}, nil
}

type QueueCounts struct {
	Pending int
	Review  int
	Done    int
	Failed  int
}

func Counts(ctx context.Context, db store.DB, p Project) (QueueCounts, error) {
	_, tx, err := db.TxReadOnly(ctx)
	if err != nil {
		return QueueCounts{}, err
	}
	defer tx.Rollback()
	f, err := eventsOf(tx, p.Key)
	if err != nil {
		return QueueCounts{}, err
	}
	f.label, f.notRepo = p.Label, p.OutsideRepo
	var out QueueCounts
	for _, ts := range f.tasks {
		switch ts.status {
		case statusPending:
			out.Pending++
		case statusReview:
			out.Review++
		case statusDone:
			out.Done++
		case statusFailed:
			out.Failed++
		}
	}
	return out, nil
}
