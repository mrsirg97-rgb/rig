CREATE INDEX IF NOT EXISTS "idx_symbols_file" ON "symbols" ("file");
CREATE INDEX IF NOT EXISTS "idx_symbols_name" ON "symbols" ("name");
CREATE INDEX IF NOT EXISTS "idx_edges_to" ON "edges" ("to_package", "to_name");
CREATE INDEX IF NOT EXISTS "idx_edges_from" ON "edges" ("from_package", "from_name");
CREATE INDEX IF NOT EXISTS "idx_edges_file" ON "edges" ("file");
CREATE VIRTUAL TABLE IF NOT EXISTS "symbol_fts" USING fts5 (
  "package",
  "name",
  "kind",
  "file",
  "line" UNINDEXED,
  "end_line" UNINDEXED
);
CREATE TABLE IF NOT EXISTS "symbol_grams" (
  "package" TEXT NOT NULL,
  "name" TEXT NOT NULL,
  "kind" TEXT NOT NULL,
  "file" TEXT NOT NULL,
  "line" INTEGER NOT NULL,
  "end_line" INTEGER NOT NULL,
  "gram" TEXT NOT NULL,
  PRIMARY KEY ("package", "name", "gram")
);
CREATE INDEX IF NOT EXISTS "idx_symbol_grams_gram" ON "symbol_grams" ("gram");
