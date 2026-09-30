package todo

const schemaJSON = `{
	"type": "object",
	"required": ["action"],
	"properties": {
		"action": {
			"enum": ["create", "claim", "start", "complete", "fail", "release", "retry", "move", "prune", "bind", "read", "note", "notes", "accept", "reject", "finished"],
			"description": "What to do."
		},
		"tasks": {
			"type": "array",
			"description": "The tasks to add, in order. Required for create. An empty list clears the queue.",
			"items": {
				"type": "object",
				"required": ["text"],
				"properties": {
					"text": {
						"type": "string",
						"description": "What needs doing."
					},
					"requires": {
						"type": ["string", "null"],
						"description": "The task this one waits for: its id (tN), its exact text, or its number in this list, where 1 is the first. Omit when none; null removes a link."
					},
					"blocks": {
						"type": ["string", "null"],
						"description": "The task that waits for this one, named the same way. Omit when none; null removes a link."
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
			"description": "The note text, or the reason when rejecting."
		},
		"status": {
			"type": "string",
			"enum": ["review"],
			"description": "For claim: review takes the next task awaiting review."
		},
		"pos": {
			"type": "integer",
			"minimum": 1,
			"description": "For move: the new position, where 1 is first."
		},
		"all": {
			"type": "boolean",
			"description": "For read: true includes finished tasks."
		},
		"n": {
			"type": "integer",
			"minimum": 1,
			"maximum": 100,
			"description": "For finished: how many to list. 10 by default, 100 at most."
		},
		"project": {
			"type": "string",
			"description": "Another workspace, as a path. Later calls act there until you name a different one. ~ expands."
		}
	}
}`

const description = "The task queue for this workspace. Guidelines: for any job of three or more steps, create the tasks " +
	"before the first edit. Start a task before working on it, complete or fail it when done, and leave the queue " +
	"empty at the end. read shows what is open. note attaches a finding to a task. claim takes the next task " +
	"nothing waits for, when other sessions share the queue. A task can wait for another: set requires on the one " +
	"that waits. Task ids (tN) come from the tool's replies: copy them, never invent them. Set project only when " +
	"the work is in a different workspace than the one you started in. Reply: the affected row and the queue's " +
	"summary, named by its workspace ([rig])."

const (
	srcProject = "project"
	srcBinding = "binding"
	srcCwd     = "cwd"
)
