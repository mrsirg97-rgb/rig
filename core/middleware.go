package core

import "context"

// ToolExec executes one tool call and reports its result. A non-nil error
// marks a fed-back failure: the result string is what the model sees, and
// the error is what bounds and accounts for the attempt.
type ToolExec func(ctx context.Context, call ToolCall) (string, error)

// ToolMiddleware wraps ToolExec, the http.Handler shape. Permissions,
// retry guards, timeouts, and logging are all this one seam, composed in
// listed order at the root: first-listed is innermost.
type ToolMiddleware func(next ToolExec) ToolExec
