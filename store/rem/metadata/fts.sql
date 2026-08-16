-- The semantic arm's substrate: pane's schema verbatim. Bookkeeping is
-- written in code in the same transaction, never by triggers (REM_SPEC E);
-- an fts-less driver is refused loudly at open, because this statement
-- rides the schema application.
CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5 (
  content,
  tokenize = 'porter unicode61'
);
