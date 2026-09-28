package todo

import (
	"encoding/json"
	"fmt"
	"sort"
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
	for _, item := range items {
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
		if ref := resolveDep(preIDs, batchTexts, f, pr.ref); ref != "" {
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

func resolveDep(preIDs map[string]bool, batchTexts map[string]*taskState, f *folded, raw string) string {
	if preIDs[raw] {
		return raw
	}
	if ts, ok := batchTexts[raw]; ok {
		return ts.id
	}
	if ts := f.byText(raw); ts != nil {
		return ts.id
	}
	return ""
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

func planCreate(f *folded, items []CreateItem) (modified []*taskState, given, fresh int, problems []string) {
	planned := map[string]*taskState{}
	type depRef struct {
		ts      *taskState
		text    string
		kind    string
		depNull bool
		dep     string
	}
	var refs []depRef
	seen := map[string]bool{}
	preIDs := depPreIDs(f)
	for _, item := range items {
		raw := item.raw()
		if raw.text == "" || seen[raw.text] {
			continue
		}
		seen[raw.text] = true
		given++
		ex := f.byText(raw.text)
		if ex != nil {
			planned[raw.text] = ex
			if raw.hasRequires {
				refs = append(refs, depRef{ts: ex, text: raw.text, kind: "requires", depNull: raw.reqNull, dep: raw.requires})
			}
			if raw.hasBlocks {
				refs = append(refs, depRef{ts: ex, text: raw.text, kind: "blocks", depNull: raw.blkNull, dep: raw.blocks})
			}
			continue
		}
		fresh++
		ts := &taskState{text: raw.text, status: statusPending}
		ts.id = f.mintID()
		ts.pos = f.nextPos()
		planned[raw.text] = ts
		f.tasks[ts.id] = ts
		modified = append(modified, ts)
		if raw.hasRequires {
			refs = append(refs, depRef{ts: ts, text: raw.text, kind: "requires", depNull: raw.reqNull, dep: raw.requires})
		}
		if raw.hasBlocks {
			refs = append(refs, depRef{ts: ts, text: raw.text, kind: "blocks", depNull: raw.blkNull, dep: raw.blocks})
		}
	}

	for _, dr := range refs {
		if dr.depNull {
			setLink(dr.ts, dr.kind, "")
			continue
		}
		if dr.dep == dr.text {
			verb := "require"
			if dr.kind == "blocks" {
				verb = "block"
			}
			addOnce(&problems, fmt.Sprintf("'%s' cannot %s itself", dr.text, verb))
			continue
		}
		if resolved := resolveDep(preIDs, planned, f, dr.dep); resolved != "" {
			setLink(dr.ts, dr.kind, resolved)
			modified = append(modified, dr.ts)
		} else {
			addOnce(&problems, fmt.Sprintf("%s '%s' not found", dr.kind, dr.dep))
		}
	}
	if path := cyclePath(f, planned); path != nil {
		problems = append(problems, "links would form a cycle: "+strings.Join(path, " -> "))
	}
	return modified, given, fresh, problems
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
