-- Outbox: one row per incident event and channel, written in the same
-- transaction as the incident change. See internal/store/doc.go.
CREATE TABLE notifications (
    id           INTEGER PRIMARY KEY,
    incident_id  INTEGER NOT NULL REFERENCES incidents (id),
    event        TEXT    NOT NULL CHECK (event IN ('opened', 'updated', 'closed')),
    at_ms        INTEGER NOT NULL,
    channel      TEXT    NOT NULL,
    incident     TEXT    NOT NULL, -- JSON snapshot of the incident at the event
    attempts     INTEGER NOT NULL DEFAULT 0,
    next_ms      INTEGER NOT NULL,
    last_error   TEXT    NOT NULL DEFAULT '',
    delivered_ms INTEGER
) STRICT;

-- An event is queued once per channel.
CREATE UNIQUE INDEX notifications_once ON notifications (incident_id, event, at_ms, channel);
CREATE INDEX notifications_due ON notifications (next_ms) WHERE delivered_ms IS NULL;
