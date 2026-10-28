package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Incident is one episode of a target out of ok, from its opening to its
// close. Its state and cause follow the target while it is open.
type Incident struct {
	ID       int64 // set by OpenIncident
	Target   string
	Scenario string
	State    string // the analyze state, for example "down"
	Cause    string // the probable cause, one sentence
	Detail   string // what the latest confirming probe measured
	Opened   time.Time
	Updated  time.Time // last change of state or cause; Opened until then
	Closed   time.Time // zero while open
	// CloseCause says why it closed. Resolved is true only when the target
	// came back to ok; an administrative close, such as a target removed from
	// the config, is never resolved and must not be announced as such.
	CloseCause string
	Resolved   bool
}

// OpenIncident stores a new open incident and returns its ID. A target has
// at most one open incident; opening a second one fails.
func (db *DB) OpenIncident(ctx context.Context, inc Incident) (int64, error) {
	res, err := db.sql.ExecContext(ctx, `INSERT INTO incidents
		(target, scenario, state, cause, detail, opened_ms, updated_ms) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		inc.Target, inc.Scenario, inc.State, inc.Cause, inc.Detail, inc.Opened.UnixMilli(), inc.Opened.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("open incident %s: %w", inc.Target, err)
	}
	return res.LastInsertId()
}

// UpdateIncident sets the state, cause and detail of an open incident.
func (db *DB) UpdateIncident(ctx context.Context, inc Incident) error {
	return db.change(ctx, "update", inc.ID, `UPDATE incidents SET state = ?, cause = ?, detail = ?, updated_ms = ?
		WHERE id = ? AND closed_ms IS NULL`, inc.State, inc.Cause, inc.Detail, inc.Updated.UnixMilli(), inc.ID)
}

// CloseIncident closes an open incident at, saying why and whether the
// target came back to ok.
func (db *DB) CloseIncident(ctx context.Context, id int64, at time.Time, cause string, resolved bool) error {
	return db.change(ctx, "close", id, `UPDATE incidents SET closed_ms = ?, close_cause = ?, resolved = ?
		WHERE id = ? AND closed_ms IS NULL`, at.UnixMilli(), cause, resolved, id)
}

// change runs an update of one open incident; none matching is an error.
func (db *DB) change(ctx context.Context, verb string, id int64, query string, args ...any) error {
	res, err := db.sql.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("%s incident %d: %w", verb, id, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("%s incident %d: %w", verb, id, ErrNoOpenIncident)
	}
	return nil
}

// OpenIncidents returns the incidents not closed yet, oldest first.
func (db *DB) OpenIncidents(ctx context.Context) ([]Incident, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT id, target, scenario, state, cause, detail, opened_ms, updated_ms
		FROM incidents WHERE closed_ms IS NULL ORDER BY opened_ms, id`)
	if err != nil {
		return nil, fmt.Errorf("query incidents: %w", err)
	}
	defer func() { _ = rows.Close() }() // errors surface through rows.Err
	var out []Incident
	for rows.Next() {
		var inc Incident
		var opened, updated int64
		if err := rows.Scan(&inc.ID, &inc.Target, &inc.Scenario, &inc.State, &inc.Cause, &inc.Detail, &opened, &updated); err != nil {
			return nil, fmt.Errorf("query incidents: %w", err)
		}
		inc.Opened, inc.Updated = time.UnixMilli(opened).UTC(), time.UnixMilli(updated).UTC()
		out = append(out, inc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query incidents: %w", err)
	}
	return out, nil
}

// incident reads one incident whatever its state, for tests.
func (db *DB) incident(ctx context.Context, id int64) (Incident, error) {
	var inc Incident
	var opened, updated int64
	var closed sql.NullInt64
	err := db.sql.QueryRowContext(ctx, `SELECT id, target, scenario, state, cause, detail, opened_ms, updated_ms,
		closed_ms, close_cause, resolved FROM incidents WHERE id = ?`, id).Scan(&inc.ID, &inc.Target, &inc.Scenario,
		&inc.State, &inc.Cause, &inc.Detail, &opened, &updated, &closed, &inc.CloseCause, &inc.Resolved)
	inc.Opened, inc.Updated = time.UnixMilli(opened).UTC(), time.UnixMilli(updated).UTC()
	if closed.Valid {
		inc.Closed = time.UnixMilli(closed.Int64).UTC()
	}
	return inc, err
}
