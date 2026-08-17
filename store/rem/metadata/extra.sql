-- What the DDL camera cannot emit (SPEC_STATE, decisions): the
-- natural-key unique index (idempotent create), the recall ordering
-- spine, and the trigram seek indexes.
CREATE UNIQUE INDEX IF NOT EXISTS memories_scope_content ON memories (scope, content_md5);
CREATE INDEX IF NOT EXISTS memories_scope_created ON memories (scope, created_at);
CREATE INDEX IF NOT EXISTS trigrams_gram_idx ON trigrams (gram);
CREATE INDEX IF NOT EXISTS trigrams_memory_idx ON trigrams (memory_id);
