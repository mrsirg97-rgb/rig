package room

import (
	"encoding/json"

	"github.com/mrsirg97-rgb/evt"
	"github.com/mrsirg97-rgb/evt/broadcast"
)

type StartChild struct {
	evt.Mark
	Model   string
	Port    int
	Command string
	Env     []string
	Log     string
}

type StopChild struct {
	evt.Mark
	Model string
	Grace int
}

type SwapChild struct {
	evt.Mark
	Model   string
	Out     string
	Port    int
	Command string
	Env     []string
	Log     string
	Grace   int
	Forced  bool
}

func (s SwapChild) Swap() bool { return true }

type TableChange struct {
	evt.Mark
	Row json.RawMessage
}

type Update struct {
	evt.Mark
	Version string
}

func jsonDeclare[T evt.Signal](reg broadcast.Registry, kind string) (broadcast.Registry, error) {
	return broadcast.Declare[T](reg, kind,
		func(v T) ([]byte, error) { return json.Marshal(v) },
		func(raw []byte) (T, error) {
			var v T
			if err := json.Unmarshal(raw, &v); err != nil {
				return v, err
			}
			return v, nil
		})
}

func Codec() broadcast.Encoder {
	reg, err := Registry()
	if err != nil {
		panic("room: " + err.Error())
	}
	return broadcast.NewJSONEncoder(reg)
}

func Registry() (broadcast.Registry, error) {
	reg, err := broadcast.Protocol()
	if err != nil {
		return nil, err
	}
	if reg, err = jsonDeclare[StartChild](reg, "room/start-child"); err != nil {
		return nil, err
	}
	if reg, err = jsonDeclare[StopChild](reg, "room/stop-child"); err != nil {
		return nil, err
	}
	if reg, err = jsonDeclare[SwapChild](reg, "room/swap-child"); err != nil {
		return nil, err
	}
	if reg, err = jsonDeclare[TableChange](reg, "room/table-change"); err != nil {
		return nil, err
	}
	return jsonDeclare[Update](reg, "room/update")
}
