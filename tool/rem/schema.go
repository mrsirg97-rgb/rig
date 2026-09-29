package rem

import ()

const schemaJSON = `{
	"type": "object",
	"required": ["action"],
	"properties": {
		"action": {
			"enum": ["learn", "recall", "reflect", "prune"]
		},
		"content": {
			"type": "string",
			"description": "Memory content (learn/reflect)"
		},
		"query": {
			"type": "string",
			"description": "Recall intent; omit to browse"
		},
		"source": {
			"type": "string",
			"description": "Raw source log for provenance (reflect)"
		},
		"kind": {
			"type": "string",
			"description": "Free-form kind label; reuse consistently"
		},
		"importance": {
			"type": "number",
			"minimum": 0,
			"maximum": 1,
			"description": "0..1; strength starts here and decays"
		},
		"scope": {
			"enum": ["project", "global", "all"]
		},
		"project": {
			"type": "string",
			"description": "another workspace, as a path: the fact belongs to it, resolved through store/scope (worktree-safe; ~ expands at the boundary)"
		},
		"k": {
			"type": "integer",
			"minimum": 1,
			"maximum": 50,
			"description": "Live-hit budget (recall)"
		},
		"verb": {
			"enum": ["remove", "reduce", "consolidate"]
		},
		"ids": {
			"type": "array",
			"items": { "type": "integer" },
			"description": "Memory ids to prune"
		},
		"older_than_days": {
			"type": "integer",
			"minimum": 1,
			"description": "Selection criterion for prune"
		},
		"supersedes": {
			"anyOf": [
				{ "type": "integer" },
				{ "type": "array", "items": { "type": "integer" } }
			]
		},
		"include_superseded": {
			"type": "boolean",
			"description": "Fill unused budget with superseded hits"
		}
	}
}`

const description = "Your memory across sessions. learn commits a fact or constraint, once. recall finds past " +
	"solutions by intent, project scope first, then global; recall without a query browses. reflect stores a " +
	"distilled memory with its source. prune removes, reduces, or consolidates. Guidelines: recall before " +
	"re-deriving a project fact; learn what the next session should not have to re-derive; supersede by id when " +
	"the code disagrees; name project when the fact belongs to a workspace other than the one you started in. Memory ids (mN) " +
	"come from the replies: copy them, never invent them. Reply: the hits with their ids and strength, or the " +
	"written row."
