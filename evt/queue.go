package evt

const (
	arity  = 4
	minCap = 8
)

type Queue interface {
	Push(e Event)
	Pop() (Event, bool)
	Peek() (Event, bool)
	Update(id uint64, priority int) bool
	View() []Event
	Len() int
}

type queue struct {
	evts      []*event
	positions map[uint64]int
}

func NewQueue(capacity int) Queue {
	if capacity < minCap {
		capacity = minCap
	}
	return &queue{evts: make([]*event, 0, capacity), positions: make(map[uint64]int, capacity)}
}

func less(a, b *event) bool {
	if a.priority != b.priority {
		return a.priority > b.priority
	}
	return a.id < b.id
}

func (q *queue) Len() int { return len(q.evts) }

func (q *queue) Push(e Event) {
	ev, ok := e.(*event)
	if !ok {
		ev = &event{id: e.ID(), priority: e.Priority(), closure: e.Closure()}
	}
	if _, dup := q.positions[ev.id]; dup {
		return
	}
	ev.index = len(q.evts)
	q.evts = append(q.evts, ev)
	q.positions[ev.id] = ev.index
	q.up(ev.index)
}

func (q *queue) Pop() (Event, bool) {
	if len(q.evts) == 0 {
		return nil, false
	}
	top := q.evts[0]
	last := len(q.evts) - 1
	q.swap(0, last)
	q.evts[last] = nil
	q.evts = q.evts[:last]
	delete(q.positions, top.id)
	top.index = -1
	if len(q.evts) > 0 {
		q.down(0)
	}
	return top, true
}

func (q *queue) Peek() (Event, bool) {
	if len(q.evts) == 0 {
		return nil, false
	}
	return q.evts[0], true
}

func (q *queue) Update(id uint64, priority int) bool {
	i, ok := q.positions[id]
	if !ok {
		return false
	}
	q.evts[i].priority = priority
	q.fix(i)
	return true
}

func (q *queue) View() []Event {
	if len(q.evts) == 0 {
		return []Event{}
	}
	clone := &queue{evts: make([]*event, len(q.evts)), positions: make(map[uint64]int, len(q.evts))}
	for i, e := range q.evts {
		c := *e
		clone.evts[i] = &c
		clone.positions[c.id] = i
	}
	out := make([]Event, 0, len(q.evts))
	for clone.Len() > 0 {
		e, _ := clone.Pop()
		out = append(out, e)
	}
	return out
}

func (q *queue) swap(i, j int) {
	q.evts[i], q.evts[j] = q.evts[j], q.evts[i]
	q.evts[i].index = i
	q.evts[j].index = j
	q.positions[q.evts[i].id] = i
	q.positions[q.evts[j].id] = j
}

func (q *queue) up(i int) {
	for i > 0 {
		parent := (i - 1) / arity
		if !less(q.evts[i], q.evts[parent]) {
			return
		}
		q.swap(i, parent)
		i = parent
	}
}

func (q *queue) down(i int) {
	n := len(q.evts)
	for {
		first := i*arity + 1
		if first >= n {
			return
		}
		best := first
		last := first + arity - 1
		if last >= n {
			last = n - 1
		}
		for child := first + 1; child <= last; child++ {
			if less(q.evts[child], q.evts[best]) {
				best = child
			}
		}
		if !less(q.evts[best], q.evts[i]) {
			return
		}
		q.swap(i, best)
		i = best
	}
}

func (q *queue) fix(i int) {
	q.up(i)
	q.down(q.evts[i].index)
}
