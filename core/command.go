package core

import "context"

// Command is a user-facing verb (SPEC_COMMANDS): one the human types, not
// one the model calls. A Frontend with command support dispatches by
// prefix before Input returns to the loop; the loop never sees a command.
// Run gets the dispatcher's context, the command's arguments (the line
// after the name, verbatim), and the env the root built (decision 2); it
// returns the reply to print (or the refusal, as an error).
type Command interface {
	Name() string
	Description() string
	Run(ctx context.Context, args string, env any) (string, error)
}
