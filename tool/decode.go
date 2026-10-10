package tool

import (
	"bytes"
	"encoding/json"
)

// Decode is the one strict door: the tool args arrive as JSON and an
// unknown field is the caller's bug, refused by name. The strict tools
// (bash, file, view, delegate, verdict) share it; the loose ones decode
// on purpose.
func Decode(data json.RawMessage, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}
