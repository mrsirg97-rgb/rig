-- The semantic arm's substrate. Bookkeeping is written in code in the
-- same transaction, never by triggers. Recall's shipped policy on
-- capability absence is to degrade to fuzzy-only. A driver that cannot
-- create the table at all fails loudly at schema application, which the
-- bundled pure-Go driver never reaches — it always ships FTS5.
CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5 (
  content,
  tokenize = 'porter unicode61'
);
