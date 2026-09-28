package todo

const schemaJSON = `{
	"type": "object",
	"required": ["action"],
	"properties": {
		"action": {
			"enum": ["create", "claim", "start", "complete", "fail", "release", "retry", "move", "prune", "bind", "read", "note", "notes", "accept", "reject", "finished"],
			"description": "The action to perform. Required."
		},
		"tasks": {
			"type": "array",
			"description": "The queue as given: create merges by text, [] clears. Required when action='create'.",
			"items": {
				"type": "object",
				"required": ["text"],
				"properties": {
					"text": {
						"type": "string",
						"description": "What needs doing"
					},
					"requires": {
						"type": ["string", "null"],
						"description": "Task id (tN) or exact text this task cannot start until done; null clears the link"
					},
					"blocks": {
						"type": ["string", "null"],
						"description": "Task id (tN) or exact text that cannot complete until this task is done; null clears the link"
					}
				}
			}
		},
		"id": {
			"type": "string",
			"description": "Task id as shown by the tool. Required for start/complete/fail/release/retry/move/note/notes/accept/reject and read with id."
		},
		"note": {
			"type": "string",
			"description": "The note text, or the reason for action='reject'. Required for action='note' and action='reject'."
		},
		"status": {
			"type": "string",
			"enum": ["review"],
			"description": "Optional claim filter: action='claim' with status='review' takes the first task in review for this session."
		},
		"pos": {
			"type": "integer",
			"minimum": 1,
			"description": "Queue position (1-based, first = 1) for action='move'."
		},
		"all": {
			"type": "boolean",
			"description": "read all:true returns the full history (done rows included); the default read is the present."
		},
		"n": {
			"type": "integer",
			"minimum": 1,
			"maximum": 100,
			"description": "How many finished rows to list for action='finished' (default 10, cap 100)."
		},
		"project": {
			"type": "string",
			"description": "the queue's project as a directory: it binds the session, whose later bare verbs then act there (worktree-safe; ~ expands)"
		}
	}
}`

const description = "the task queue for the session's project. Guidelines: any job of three or more steps -> " +
	"create before the first edit (tasks: [{text, requires?, blocks?}]); requires = I wait for it; blocks = " +
	"it waits for me; claim takes the next pending task nothing waits for, complete lands your task done " +
	"here (solo); a worker (rig -p: delegate, swarm) is read/note-only — the supervisor owns the board, and " +
	"the worker's findings go in note and rem; accept or reject a task in review — the parent's flow is " +
	"read then accept/reject, an unowned review task auto-claims, a foreign hold refuses, and reject takes " +
	"the reason as note; note attaches a message to any task; notes with id lists a task's notes in order " +
	"with their session and time, read shows the count and read with id points at notes; read shows the " +
	"present (all:true for history); finished lists the n most recent finished, newest first, default 10, " +
	"cap 100; move reorders by a 1-based pos; prune drops the done rows. " +
	"Every reply names the queue it acted on ([rig]); name project when the work is in a repo you did not " +
	"start in, which binds the session. Reply: the affected row and the summary; a refusal names the rule. " +
	"Ids (tN) are minted by the tool — copy, never invent."

const (
	srcProject = "project"
	srcBinding = "binding"
	srcCwd     = "cwd"
	srcHost    = "host"
)
