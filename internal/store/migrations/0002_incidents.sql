-- One row per incident: from a target leaving ok to its close. See internal/store/doc.go.
CREATE TABLE incidents (
    id          INTEGER PRIMARY KEY,
    target      TEXT    NOT NULL,
    scenario    TEXT    NOT NULL,
    state       TEXT    NOT NULL,
    cause       TEXT    NOT NULL,
    detail      TEXT    NOT NULL,
    opened_ms   INTEGER NOT NULL,
    updated_ms  INTEGER NOT NULL,
    closed_ms   INTEGER,
    close_cause TEXT    NOT NULL DEFAULT '',
    resolved    INTEGER NOT NULL DEFAULT 0 CHECK (resolved IN (0, 1))
) STRICT;

-- At most one open incident per target.
CREATE UNIQUE INDEX incidents_open ON incidents (target) WHERE closed_ms IS NULL;
CREATE INDEX incidents_opened ON incidents (opened_ms);
