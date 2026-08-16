-- The semantic arm's substrate: pane's schema verbatim. Bookkeeping is
-- written in code in the same transaction, never by triggers (REM_SPEC E).
-- Recall's shipped policy on capability absence is REM_SPEC's: degrade to
-- fuzzy-only (the named degradation case). A driver that cannot create
-- the table at all fails loudly at schema application, which the bundled
-- pure-Go driver never reaches — it always ships FTS5.
CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5 (
  content,
  tokenize = 'porter unicode61'
);
