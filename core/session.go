package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// FileState pins what a file looked like at last read, so edit-after-
// external-change fails loudly instead of clobbering. Mtime is unix
// nanoseconds: JSON-stable, timezone-free.
type FileState struct {
	Hash  string
	Mtime int64
}

// Session is the turn's state: the transcript plus file provenance.
// In-memory for v1, JSON-serializable from day one.
type Session struct {
	Messages []Message
	Files    map[string]FileState // path -> state at last read
}

func NewSession() *Session {
	return &Session{Files: map[string]FileState{}}
}

func (s *Session) Append(m Message) {
	s.Messages = append(s.Messages, m)
}

// Save writes the session as JSON to path.
func (s *Session) Save(path string) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("session save: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("session save: %w", err)
	}
	return nil
}

// Load reads a session saved by Save.
func Load(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("session load: %w", err)
	}
	s := &Session{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(s); err != nil {
		return nil, fmt.Errorf("session load: %w", err)
	}
	if s.Files == nil {
		s.Files = map[string]FileState{}
	}
	return s, nil
}

// context threading. ToolExec's ctx is the only channel the loop controls;
// the session rides it so file tools maintain FileState without reaching
// around the Tool interface.
type contextKey int

const sessionKey contextKey = 1

// WithSession threads the session into ctx for the execution chain.
func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionKey, s)
}

// SessionFrom recovers the session threaded by WithSession.
func SessionFrom(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(sessionKey).(*Session)
	return s, ok
}

var ErrNoSession = errors.New("session not threaded")
