-- One row per turn of one probe. See internal/store/doc.go.
CREATE TABLE samples (
    id                 INTEGER PRIMARY KEY,
    probe_id           TEXT    NOT NULL,
    ts_ms              INTEGER NOT NULL,
    target             TEXT    NOT NULL,
    scenario           TEXT    NOT NULL,
    turn               INTEGER NOT NULL CHECK (turn >= 1),
    ttft_us            INTEGER,
    total_us           INTEGER NOT NULL,
    input_tokens       INTEGER NOT NULL,
    output_tokens      INTEGER NOT NULL,
    http_status        INTEGER NOT NULL,
    provider_error     TEXT    NOT NULL,
    error              TEXT    NOT NULL,
    stream_cut         INTEGER NOT NULL CHECK (stream_cut IN (0, 1)),
    cache_read_tokens  INTEGER NOT NULL,
    cache_write_tokens INTEGER NOT NULL,
    expect_ok          INTEGER NOT NULL CHECK (expect_ok IN (0, 1))
) STRICT;

CREATE INDEX samples_target_ts ON samples (target, ts_ms);
CREATE INDEX samples_ts ON samples (ts_ms);
CREATE INDEX samples_probe ON samples (probe_id);
