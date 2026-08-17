-- What the DDL camera cannot emit (SPEC_STATE, decisions): the
-- natural-key unique index (idempotent create) and the ordering spine.
CREATE UNIQUE INDEX IF NOT EXISTS tasks_text_unique ON tasks (text);
CREATE INDEX IF NOT EXISTS tasks_pos_seq ON tasks (pos, created_seq);
