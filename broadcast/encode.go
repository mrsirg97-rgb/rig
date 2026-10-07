package broadcast

import (
	"encoding/json"
	"fmt"

	"github.com/mrsirg97-rgb/rig/v2/core"
)

/*
Encoder

	Encoder provides the means for a Transport that crosses a process boundary to serialize and deserialize the Room Messages.
	the kind names the core event the payload is; a frame with no kind is a heartbeat.
*/
type Encoder interface {
	Encode(message Message) ([]byte, error)
	Decode(encoded []byte) (Message, error)
}

type encoder struct{}

type JSONMessage struct {
	Origin  int64           `json:"origin"`
	Ok      bool            `json:"ok"`
	Kind    string          `json:"kind,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func NewJSONEncoder() Encoder {
	return &encoder{}
}

func (e *encoder) Encode(message Message) ([]byte, error) {
	frame := JSONMessage{Origin: message.Origin(), Ok: message.Ok()}
	if ev := message.Event(); ev != nil {
		kind, ok := kindOf(ev)
		if !ok {
			return nil, fmt.Errorf("broadcast: %T does not cross a transport", ev)
		}
		payload, err := json.Marshal(ev)
		if err != nil {
			return nil, err
		}
		frame.Kind, frame.Payload = kind, payload
	}
	return json.Marshal(frame)
}

func (e *encoder) Decode(encoded []byte) (Message, error) {
	var frame JSONMessage
	if err := json.Unmarshal(encoded, &frame); err != nil {
		return nil, err
	}
	if frame.Kind == "" {
		return NewMessage(frame.Origin, frame.Ok), nil
	}
	ev, err := eventOf(frame.Kind, frame.Payload)
	if err != nil {
		return nil, err
	}
	return NewMessage(frame.Origin, frame.Ok, ev), nil
}

const (
	kindNotice      = "notice"
	kindSwarmStatus = "swarm_status"
	kindVerdict     = "verdict"
	kindPhase       = "phase"
	kindReasoning   = "reasoning"
	kindToolStart   = "tool_start"
)

func kindOf(ev core.Event) (string, bool) {
	switch ev.(type) {
	case core.Notice:
		return kindNotice, true
	case core.SwarmStatus:
		return kindSwarmStatus, true
	case core.Verdict:
		return kindVerdict, true
	case core.Phase:
		return kindPhase, true
	case core.ReasoningDelta:
		return kindReasoning, true
	case core.ToolStart:
		return kindToolStart, true
	}
	return "", false
}

func eventOf(kind string, payload json.RawMessage) (core.Event, error) {
	switch kind {
	case kindNotice:
		var ev core.Notice
		return ev, json.Unmarshal(payload, &ev)
	case kindSwarmStatus:
		var ev core.SwarmStatus
		return ev, json.Unmarshal(payload, &ev)
	case kindVerdict:
		var ev core.Verdict
		return ev, json.Unmarshal(payload, &ev)
	case kindPhase:
		var ev core.Phase
		return ev, json.Unmarshal(payload, &ev)
	case kindReasoning:
		var ev core.ReasoningDelta
		return ev, json.Unmarshal(payload, &ev)
	case kindToolStart:
		var ev core.ToolStart
		return ev, json.Unmarshal(payload, &ev)
	}
	return nil, fmt.Errorf("broadcast: unknown kind %q", kind)
}
