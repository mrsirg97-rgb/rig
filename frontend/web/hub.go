package web

import (
	"encoding/json"
)

const (
	ringCap       = 4096
	coalesceBytes = 4096
)

func (c *chat) publish(f map[string]any) {
	c.hub.Lock()
	defer c.hub.Unlock()
	c.seq++
	f["seq"] = c.seq
	kind, _ := f["kind"].(string)
	if (kind == "text" || kind == "reasoning") && c.last != nil && c.last["kind"] == kind {
		prev, _ := c.last["text"].(string)
		next, _ := f["text"].(string)
		if len(prev)+len(next) <= coalesceBytes {
			c.last["text"] = prev + next
			c.last["seq"] = c.seq
			c.ring[len(c.ring)-1] = mustJSON(c.last)
			c.send(mustJSON(f))
			return
		}
	}
	c.last = f
	raw := mustJSON(f)
	c.ring = append(c.ring, raw)
	if len(c.ring) > ringCap {
		c.ring = c.ring[len(c.ring)-ringCap:]
	}
	c.send(raw)
}

func (c *chat) send(raw []byte) {
	for ch := range c.subs {
		select {
		case ch <- raw:
		default:
		}
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"kind":"fault","text":"frame did not marshal"}`)
	}
	return b
}

func (c *chat) subscribe(since int64) (chan []byte, [][]byte, int64) {
	c.hub.Lock()
	defer c.hub.Unlock()
	ch := make(chan []byte, 256)
	c.subs[ch] = struct{}{}
	var replay [][]byte
	for _, raw := range c.ring {
		var head struct {
			Seq int64 `json:"seq"`
		}
		if json.Unmarshal(raw, &head) == nil && head.Seq > since {
			replay = append(replay, raw)
		}
	}
	return ch, replay, c.seq
}

func (c *chat) unsubscribe(ch chan []byte) {
	c.hub.Lock()
	delete(c.subs, ch)
	c.hub.Unlock()
}
