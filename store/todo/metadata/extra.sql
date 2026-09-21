-- What the DDL camera cannot emit (SPEC_STATE, decisions): the
-- natural-key unique index (idempotent create) and the ordering spine.
-- Both are scoped: text uniqueness and the ordering spine are per
-- queue, since ids are tN per scope.
CREATE UNIQUE INDEX IF NOT EXISTS tasks_text_unique ON tasks (scope, text);
CREATE INDEX IF NOT EXISTS tasks_pos_seq ON tasks (scope, pos, created_seq);

-- The session's queue binding: which scope a session's bare todo verbs
-- act on. Keyed by session, not by scope: the question is "whose queue
-- is this session in", and the answer must survive a resume from another
-- directory. Mutable state beside the log, like meta: the log stays the
-- spine of the queue, the binding only says which spine a session reads.
CREATE TABLE IF NOT EXISTS session_project (
  session_id TEXT NOT NULL PRIMARY KEY,
  scope TEXT NOT NULL,
  label TEXT NOT NULL,
  outside_repo INTEGER NOT NULL,
  bound_at TEXT NOT NULL
);
