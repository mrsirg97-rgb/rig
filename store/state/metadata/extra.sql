-- What the DDL camera cannot emit: the seek paths the session listing
-- reads by. ListSessions aggregates per session (turns after the last
-- summary, faults, tokens, cost). Without these every aggregate is a
-- scan of messages per session: 10s on a 170-session, 24k-message
-- store, measured, and the dashboard's read timeout trips.
CREATE INDEX IF NOT EXISTS messages_session_role_seq ON messages (session_id, role, seq);
CREATE INDEX IF NOT EXISTS faults_session ON faults (session_id);
