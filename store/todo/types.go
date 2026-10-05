package todo

import (
	"fmt"
	tododdl "github.com/mrsirg97-rgb/rig/v2/store/todo/ddl"
	todometa "github.com/mrsirg97-rgb/rig/v2/store/todo/metadata"
	"time"
)

const SchemaVersion = 4

func DDL() []string { return tododdl.Statements() }

func Statements() []string {
	out := tododdl.Statements()
	return append(out, todometa.ExtraStatements()...)
}

const anon = "anon"

const StaleClaimAfter = 24 * time.Hour

const DefaultFinishedShown = 10

const FinishedListCap = 100

type Project struct {
	Key         string
	Label       string
	OutsideRepo bool
	Dir         string
}

type CreateItem struct {
	Text         string
	Requires     *string
	RequiresNull bool
	Blocks       *string
	BlocksNull   bool
}

func (c CreateItem) raw() rawItem {
	it := rawItem{text: c.Text}
	if c.RequiresNull {
		it.hasRequires, it.reqNull = true, true
	} else if c.Requires != nil {
		it.hasRequires = true
		it.requires = *c.Requires
	}
	if c.BlocksNull {
		it.hasBlocks, it.blkNull = true, true
	} else if c.Blocks != nil {
		it.hasBlocks = true
		it.blocks = *c.Blocks
	}
	return it
}

type noteState struct {
	text    string
	session string
	ts      string
}

type taskState struct {
	id          string
	text        string
	status      string
	pos         int
	requires    string
	blocks      string
	owner       string
	createdSeq  int64
	updatedSeq  int64
	finishedSeq int64
	updatedTs   string
	notes       []noteState
}

type folded struct {
	compactSeq int64
	tasks      map[string]*taskState
	maxSeq     int64
	maxPos     int
	maxIdNum   int
	globalSeq  int64
	label      string
}

func newFolded() *folded {
	return &folded{tasks: map[string]*taskState{}}
}

func (f *folded) byText(text string) *taskState {
	for _, ts := range f.tasks {
		if ts.text == text {
			return ts
		}
	}
	return nil
}

func (f *folded) mintID() string {
	for {
		f.maxIdNum++
		id := fmt.Sprintf("t%d", f.maxIdNum)
		if _, ok := f.tasks[id]; !ok {
			return id
		}
	}
}

func (f *folded) nextPos() int {
	f.maxPos++
	return f.maxPos
}

func (f *folded) nextSeq() int64 {
	f.globalSeq++
	return f.globalSeq
}

type eventRow struct {
	seq     int64
	op      string
	args    string
	session string
	ts      string
	scope   string
}
