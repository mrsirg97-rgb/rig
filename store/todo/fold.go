package todo

import (
	"encoding/json"
)

func attrOf(e eventRow) string {
	if e.session == "" {
		return anon
	}
	return e.session
}

func (f *folded) apply(e eventRow) {
	if e.seq > f.maxSeq {
		f.maxSeq = e.seq
	}
	if e.seq > f.globalSeq {
		f.globalSeq = e.seq
	}
	switch e.op {
	case "create":
		f.applyCreate(e)
	case "start", "complete", "fail", "release", "retry":
		f.applyVerb(e)
	case "claim":
		f.applyClaimEvent(e)
	case "note":
		f.applyNoteEvent(e)
	case "accept":
		f.applyAcceptEvent(e)
	case "reject":
		f.applyRejectEvent(e)
	case "move":
		f.applyMoveEvent(e)
	case "prune":
		f.applyPrune()
	case "compact":
		f.applyCompactEvent(e)
	}
}

func (f *folded) applyClaimEvent(e eventRow) {
	var payload struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(e.args), &payload) != nil || payload.ID == "" {
		return
	}
	ts, ok := f.tasks[payload.ID]
	if !ok {
		return
	}
	switch ts.status {
	case statusPending:
		ts.status = statusActive
		ts.owner = attrOf(e)
	case statusReview:
		ts.owner = attrOf(e)
	default:
		return
	}
	ts.updatedSeq = e.seq
	ts.updatedTs = e.ts
}

func (f *folded) applyNoteEvent(e eventRow) {
	var payload struct {
		ID   string `json:"id"`
		Note string `json:"note"`
	}
	if json.Unmarshal([]byte(e.args), &payload) != nil || payload.ID == "" || payload.Note == "" {
		return
	}
	ts, ok := f.tasks[payload.ID]
	if !ok {
		return
	}
	ts.notes = append(ts.notes, noteState{text: payload.Note, session: attrOf(e), ts: e.ts})
}

func (f *folded) applyAcceptEvent(e eventRow) {
	var payload struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(e.args), &payload) != nil || payload.ID == "" {
		return
	}
	ts, ok := f.tasks[payload.ID]
	if !ok || ts.status != statusReview {
		return
	}
	ts.status = statusDone
	ts.owner = ""
	ts.updatedSeq = e.seq
	ts.finishedSeq = e.seq
	ts.updatedTs = e.ts
}

func (f *folded) applyRejectEvent(e eventRow) {
	var payload struct {
		ID   string `json:"id"`
		Note string `json:"note"`
	}
	if json.Unmarshal([]byte(e.args), &payload) != nil || payload.ID == "" {
		return
	}
	ts, ok := f.tasks[payload.ID]
	if !ok || ts.status != statusReview {
		return
	}
	ts.status = statusPending
	ts.owner = ""
	if payload.Note != "" {
		ts.notes = append(ts.notes, noteState{text: payload.Note, session: attrOf(e)})
	}
	ts.updatedSeq = e.seq
	ts.updatedTs = e.ts
}
