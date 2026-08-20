package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

type FileState struct {
	Hash  string
	Mtime int64
}

type Session struct {
	ID       string
	Messages []Message
	Files    map[string]FileState
}

func NewSession() *Session {
	return &Session{ID: mintSessionID(), Files: map[string]FileState{}}
}

func mintSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("core: session id: %v", err))
	}
	return fmt.Sprintf("%x%x", time.Now().UnixMilli(), b)
}

func (s *Session) Append(m Message) {
	s.Messages = append(s.Messages, m)
}

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

type contextKey int

const sessionKey contextKey = 1

func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionKey, s)
}

func SessionFrom(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(sessionKey).(*Session)
	return s, ok
}

var ErrNoSession = errors.New("session not threaded")
