// Package guard bounds the repetition of fed-back failures. Per the spec:
// every call executes exactly once, always; what is bounded is the model's
// re-issuance of an identical failing call, counted across turns. After
// `limit` failures of the same call, the next identical issuance is refused
// without executing, naming the bound. Successful re-issuance (polling)
// never counts and stays unbounded. Sequential delivery means no locking.
package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/mrsirg97-rgb/looper/core"
)

// Retry bounds the repetition of failing calls, keyed by name plus args
// digest.
func Retry(limit int) core.ToolMiddleware {
	if limit < 1 {
		limit = 1
	}
	failures := map[string]int{}

	keyOf := func(call core.ToolCall) string {
		buf := make([]byte, 0, len(call.Name)+1+len(call.Args))
		buf = append(buf, call.Name...)
		buf = append(buf, 0)
		buf = append(buf, call.Args...)
		sum := sha256.Sum256(buf)
		return hex.EncodeToString(sum[:])
	}

	return func(next core.ToolExec) core.ToolExec {
		return func(ctx context.Context, call core.ToolCall) (string, error) {
			key := keyOf(call)
			if failures[key] >= limit {
				msg := fmt.Sprintf("retry bound exhausted: %s has failed %d times; stop reissuing this call", call.Name, limit)
				return msg, errors.New(msg)
			}
			content, err := next(ctx, call)
			if err != nil {
				failures[key]++
			}
			return content, err
		}
	}
}
