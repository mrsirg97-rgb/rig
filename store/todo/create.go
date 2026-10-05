package todo

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type rawItem struct {
	text        string
	hasRequires bool
	reqNull     bool
	requires    string
	hasBlocks   bool
	blkNull     bool
	blocks      string
}

func linkField(raw map[string]any, key, oldKey string) (has, isNull bool, value string) {
	if v, ok := raw[key]; ok {
		has = true
		if v == nil {
			isNull = true
		} else if s, ok := v.(string); ok {
			value = s
		}
		return
	}
	if v, ok := raw[oldKey]; ok {
		has = true
		if v == nil {
			isNull = true
		} else if s, ok := v.(string); ok {
			value = s
		}
	}
	return
}

func decodeItems(args string) ([]rawItem, bool) {
	var payload struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if json.Unmarshal([]byte(args), &payload) != nil {
		return nil, false
	}
	if payload.Tasks == nil {
		var one map[string]any
		if json.Unmarshal([]byte(args), &one) != nil || one["text"] == nil {
			return nil, false
		}
		payload.Tasks = []map[string]any{one}
	}
	var out []rawItem
	for _, raw := range payload.Tasks {
		text, _ := raw["text"].(string)
		it := rawItem{text: text}
		it.hasRequires, it.reqNull, it.requires = linkField(raw, "requires", "dependsOn")
		it.hasBlocks, it.blkNull, it.blocks = linkField(raw, "blocks", "")
		out = append(out, it)
	}
	return out, true
}

func (f *folded) applyCreate(e eventRow) {
	items, ok := decodeItems(e.args)
	if !ok {
		return
	}
	if len(items) == 0 {
		f.tasks = map[string]*taskState{}
		return
	}
	type pendingRef struct {
		ts    *taskState
		kind  string
		clear bool
		ref   string
	}
	var refs []pendingRef
	seen := map[string]bool{}
	preIDs := depPreIDs(f)
	batchTexts := map[string]*taskState{}
	order := make([]string, 0, len(items))
	for _, item := range items {
		order = append(order, item.text)
		if item.text == "" || seen[item.text] {
			continue
		}
		seen[item.text] = true
		ex := f.byText(item.text)
		if ex != nil {
			if item.hasRequires {
				refs = append(refs, pendingRef{ts: ex, kind: "requires", clear: item.reqNull, ref: item.requires})
			}
			if item.hasBlocks {
				refs = append(refs, pendingRef{ts: ex, kind: "blocks", clear: item.blkNull, ref: item.blocks})
			}
			continue
		}
		ts := &taskState{
			text: item.text, status: statusPending,
			createdSeq: e.seq, updatedSeq: e.seq, updatedTs: e.ts,
		}
		ts.id = f.mintID()
		ts.pos = f.nextPos()
		f.tasks[ts.id] = ts
		batchTexts[item.text] = ts
		if item.hasRequires {
			refs = append(refs, pendingRef{ts: ts, kind: "requires", clear: item.reqNull, ref: item.requires})
		}
		if item.hasBlocks {
			refs = append(refs, pendingRef{ts: ts, kind: "blocks", clear: item.blkNull, ref: item.blocks})
		}
	}

	for _, pr := range refs {
		if pr.clear {
			setLink(pr.ts, pr.kind, "")
			continue
		}
		if ref := resolveDep(preIDs, batchTexts, order, f, pr.ref); ref != "" && ref != pr.ts.id {
			setLink(pr.ts, pr.kind, ref)
		}
	}
}

func setLink(ts *taskState, kind, ref string) {
	if kind == "blocks" {
		ts.blocks = ref
		return
	}
	ts.requires = ref
}

func depPreIDs(f *folded) map[string]bool {
	out := map[string]bool{}
	for id := range f.tasks {
		out[id] = true
	}
	return out
}

func resolveDep(preIDs map[string]bool, batchTexts map[string]*taskState, order []string, f *folded, raw string) string {
	if preIDs[raw] {
		return raw
	}
	if ts, ok := batchTexts[raw]; ok {
		return ts.id
	}
	if ts := f.byText(raw); ts != nil {
		return ts.id
	}
	if n, ok := batchPosition(raw, len(order)); ok {
		if ts, ok := batchTexts[order[n-1]]; ok {
			return ts.id
		}
		if ts := f.byText(order[n-1]); ts != nil {
			return ts.id
		}
	}
	return ""
}

func batchPosition(raw string, size int) (int, bool) {
	if raw == "" || strings.TrimLeft(raw, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > size {
		return 0, false
	}
	return n, true
}

func (f *folded) applyVerb(e eventRow) {
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
	switch e.op {
	case "start":
		if ts.status == "pending" {
			ts.status = "in_progress"
			ts.owner = attrOf(e)
		}
	case "complete":
		if ts.status == "in_progress" {
			ts.status = "review"
			ts.owner = ""
		}
	case "fail":
		if ts.status == "in_progress" {
			ts.status = "failed"
			ts.owner = ""
		} else if ts.status == "review" {
			ts.status = "failed"
			ts.owner = ""
		}
	case "release":
		if ts.status == "in_progress" {
			ts.status = "pending"
			ts.owner = ""
		} else if ts.status == "review" {
			ts.owner = ""
		}
	case "retry":
		if ts.status == "failed" {
			ts.status = "pending"
		}
	}
	ts.updatedSeq = e.seq
	ts.updatedTs = e.ts
}

func planCreate(f *folded, item CreateItem) (modified []*taskState, note string, problems []string) {
	raw := item.raw()
	ts := f.byText(raw.text)
	if ts == nil {
		ts = &taskState{text: raw.text, status: statusPending}
		ts.id = f.mintID()
		ts.pos = f.nextPos()
		f.tasks[ts.id] = ts
		modified = append(modified, ts)
		note = "added " + ts.id
	} else {
		note = ts.id + " already there"
	}
	for _, link := range []struct {
		has, clear bool
		kind, ref  string
	}{
		{raw.hasRequires, raw.reqNull, "requires", raw.requires},
		{raw.hasBlocks, raw.blkNull, "blocks", raw.blocks},
	} {
		if !link.has {
			continue
		}
		if link.clear {
			setLink(ts, link.kind, "")
			modified = append(modified, ts)
			continue
		}
		if link.ref == ts.id {
			verb := "require"
			if link.kind == "blocks" {
				verb = "block"
			}
			addOnce(&problems, fmt.Sprintf("'%s' cannot %s itself", raw.text, verb))
			continue
		}
		if _, ok := f.tasks[link.ref]; !ok {
			addOnce(&problems, fmt.Sprintf("%s '%s' not found", link.kind, link.ref))
			continue
		}
		setLink(ts, link.kind, link.ref)
		modified = append(modified, ts)
	}
	if path := cyclePath(f, map[string]*taskState{raw.text: ts}); path != nil {
		problems = append(problems, "links would form a cycle: "+strings.Join(path, " -> "))
	}
	return modified, note, problems
}

func addOnce(list *[]string, s string) {
	for _, have := range *list {
		if have == s {
			return
		}
	}
	*list = append(*list, s)
}

func cyclePath(f *folded, planned map[string]*taskState) []string {
	adj := map[string][]string{}
	for id, ts := range f.tasks {
		if ts.requires != "" {
			adj[id] = append(adj[id], ts.requires)
		}
		if ts.blocks != "" {
			adj[ts.blocks] = append(adj[ts.blocks], id)
		}
	}
	var cycle []string
	var stack []string
	onStack := map[string]bool{}
	var dfs func(id string) bool
	dfs = func(id string) bool {
		onStack[id] = true
		stack = append(stack, id)
		for _, dep := range adj[id] {
			if onStack[dep] {
				for i, n := range stack {
					if n == dep {
						cycle = append(append([]string{}, stack[i:]...), dep)
						return true
					}
				}
				continue
			}
			if _, ok := f.tasks[dep]; !ok {
				continue
			}
			if dfs(dep) {
				return true
			}
		}
		onStack[id] = false
		stack = stack[:len(stack)-1]
		return false
	}
	var ids []string
	for id := range planned {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ts := planned[id]
		if dfs(ts.id) {
			return cycle
		}
	}
	return nil
}

const linkForms = " (a link is a task id from a reply, \"t12\"; create the task first, then link to the id it was given)"

func linkFormsHint(problems []string) string {
	for _, problem := range problems {
		if strings.HasSuffix(problem, " not found") {
			return linkForms
		}
	}
	return ""
}
