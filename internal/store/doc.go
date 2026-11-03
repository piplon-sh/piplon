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
// as NULL so failures never skew latency percentiles. Throughput is not
// stored: Sample.TokensPerSecond derives it, and declines below 10 output
// tokens or 50 ms of generation, where the figure would be noise.
//
// Insert writes a batch atomically, Samples reads with optional target and
// time window filters, SamplesAfter reads the samples stored after a given ID
// so a reader can follow new probes, LatestProbes returns each target's most
// recent probe, Ping checks the database is readable, Prune deletes samples older than a cutoff, and
// RunRetention prunes once at start and then on every tick until its context
// is cancelled.
//
// An Incident is one episode of a target out of ok. A target has at most one
// open incident, enforced by a partial unique index. OpenIncident stores it,
// UpdateIncident follows its state and cause while open, CloseIncident closes
// it with a close cause and Resolved, OpenIncidents lists the open ones, and
// Incidents those open at some point since a given time, newest first.
// Resolved is true only when the target came back to ok: an administrative
// close, such as a target removed from the config, is not a resolution and
// must never be announced as one. Updating or closing an incident that is not
// open returns ErrNoOpenIncident. Incidents are not pruned by retention.
//
// The notifications table is an outbox. OpenIncident, UpdateIncident and
// CloseIncident take the channels to notify and, in the same transaction as
// the incident change, queue one Notification per channel with a JSON
// snapshot of the incident as the event left it: a crash cannot lose an event
// nor queue one for a change that did not happen, and a unique index on
// (incident, event, instant, channel) queues each event once. Delivery is at
// least once: a crash between a send and Delivered sends it again. Due lists
// the pending notifications whose next attempt has come, never one behind a
// pending earlier event of the same incident and channel. Delivered marks one
// sent, Failed counts an attempt and sets the next one, and Abandon gives up
// on one, which lets the next event of that incident through. Outbox counts
// each channel's notifications by status, with the oldest pending one.
//
// A TargetState is a target's current state as piplon run judges it, one row
// per target: the confirmed state, since when, its cause, what the latest
// probe measured and the open incident. SaveTargetState replaces it;
// OpenIncident, UpdateIncident and CloseIncident take one to save in their
// transaction, so the state and the incident never disagree. TargetStates
// lists them, and ForgetTargets deletes those of targets no longer
// configured.
//
// Store is the interface the rest of piplon depends on; *DB is its SQLite
// implementation.
package store
