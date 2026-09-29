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
			"description": "The task, as tN from a reply. Required for every action but create and read."
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

const description = "The task queue for this repo. Guidelines: for any job of three or more steps, create the tasks " +
	"before the first edit, start one before working on it, complete or fail it when done, and leave the queue " +
	"empty at the end. read shows what is open. note attaches a finding to a task. claim takes the next task " +
	"nothing waits for, when other sessions share the queue. requires links a task to one it waits for; blocks " +
	"links it to one that waits for it. Task ids (tN) come from the tool's replies: copy them, never invent " +
	"them. Set project only when the repo differs from the one you started in. " +
	"Reply: the affected row and the queue's summary, named by its repo ([rig])."

const (
	srcProject = "project"
	srcBinding = "binding"
	srcCwd     = "cwd"
	srcHost    = "host"
)
