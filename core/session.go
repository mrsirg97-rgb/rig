package core

import (
	"context"
	"crypto/rand"
	"fmt"
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

type contextKey int

const sessionKey contextKey = 1

func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionKey, s)
}

func SessionFrom(ctx context.Context) (*Session, bool) {
	s, ok := ctx.Value(sessionKey).(*Session)
	return s, ok
}
