// Package store persists probe samples in an embedded SQLite database
// (modernc.org/sqlite, no cgo).
//
// Open creates or opens the file, enables WAL and a 5 s busy timeout, and
// applies the embedded migrations in migrations/NNNN_name.sql, tracking the
// schema version in PRAGMA user_version. A database written by a newer piplon
// is refused with ErrSchemaTooNew rather than modified.
//
// One Sample is one turn of one probe: a 3-turn scenario yields up to three
// rows sharing a ProbeID. Durations are stored in microseconds and times in
// UTC milliseconds. A TTFT of 0 means no content delta arrived and is stored
// as NULL so failures never skew latency percentiles.
//
// Insert writes a batch atomically, Samples reads with optional target and
// time window filters, Prune deletes samples older than a cutoff, and
// RunRetention prunes once at start and then on every tick until its context
// is cancelled. Store is the interface the rest of piplon depends on; *DB is
// its SQLite implementation.
package store
