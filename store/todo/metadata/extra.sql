-- What the DDL camera cannot emit (SPEC_STATE, decisions): the
-- natural-key unique index (idempotent create) and the ordering spine.
-- Both are scoped: text uniqueness and the ordering spine are per
-- queue, since ids are tN per scope.
CREATE UNIQUE INDEX IF NOT EXISTS tasks_text_unique ON tasks (scope, text);
CREATE INDEX IF NOT EXISTS tasks_pos_seq ON tasks (scope, pos, created_seq);
