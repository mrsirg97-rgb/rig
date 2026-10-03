CREATE INDEX IF NOT EXISTS "idx_symbols_file" ON "symbols" ("file");
CREATE INDEX IF NOT EXISTS "idx_symbols_name" ON "symbols" ("name");
CREATE INDEX IF NOT EXISTS "idx_edges_to" ON "edges" ("to_package", "to_name");
CREATE INDEX IF NOT EXISTS "idx_edges_from" ON "edges" ("from_package", "from_name");
CREATE INDEX IF NOT EXISTS "idx_edges_file" ON "edges" ("file");
