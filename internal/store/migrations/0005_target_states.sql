-- The current state of each configured target, as piplon run judges it.
-- Written with every probe; in the same transaction as the incident change
-- when there is one. See internal/store/doc.go.
CREATE TABLE target_states (
    target      TEXT    PRIMARY KEY,
    scenario    TEXT    NOT NULL,
    state       TEXT    NOT NULL, -- confirmed state, after hysteresis
    since_ms    INTEGER NOT NULL, -- when that state began
    cause       TEXT    NOT NULL, -- probable cause of that state, '' for ok
    last_state  TEXT    NOT NULL, -- verdict of the latest probe, may differ while not yet confirmed
    last_detail TEXT    NOT NULL, -- what the latest probe measured
    last_ms     INTEGER NOT NULL, -- when the latest probe started
    incident_id INTEGER REFERENCES incidents (id) -- the open incident, NULL if none
) STRICT;
