-- What the DDL camera cannot emit (SPEC_DECISION): the indexes the
-- reads need. The reviewer drains by status. The learning reads scan a
-- project's rows in time order.
CREATE INDEX IF NOT EXISTS decisions_status ON decisions (status);
CREATE INDEX IF NOT EXISTS decisions_scope_ts ON decisions (scope, ts);
