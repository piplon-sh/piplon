package store

import (
	"context"
	"database/sql"
	"encoding/json"
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

// Incident events, as queued in the outbox.
const (
	EventOpened  = "opened"
	EventUpdated = "updated"
	EventClosed  = "closed"
)

// OpenIncident stores a new open incident, queues its opening for each of
// channels in the same transaction, and returns its ID. A target has at most
// one open incident; opening a second one fails. A non-nil state is saved in
// the same transaction, with the new incident's ID.
func (db *DB) OpenIncident(ctx context.Context, inc Incident, channels []string, state *TargetState) (int64, error) {
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO incidents
			(target, scenario, state, cause, detail, opened_ms, updated_ms) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			inc.Target, inc.Scenario, inc.State, inc.Cause, inc.Detail, inc.Opened.UnixMilli(), inc.Opened.UnixMilli())
		if err != nil {
			return err
		}
		if inc.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		inc.Updated = inc.Opened
		if state != nil {
			state.IncidentID = inc.ID
		}
		return queue(ctx, tx, EventOpened, inc.Opened, inc, channels, state)
	})
	if err != nil {
		return 0, fmt.Errorf("open incident %s: %w", inc.Target, err)
	}
	return inc.ID, nil
}

// UpdateIncident sets the state, cause, detail and Updated of an open
// incident and queues the update for each of channels. A non-nil state is
// saved in the same transaction.
func (db *DB) UpdateIncident(ctx context.Context, inc Incident, channels []string, state *TargetState) error {
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		if err := changeOne(ctx, tx, `UPDATE incidents SET state = ?, cause = ?, detail = ?, updated_ms = ?
			WHERE id = ? AND closed_ms IS NULL`, inc.State, inc.Cause, inc.Detail, inc.Updated.UnixMilli(), inc.ID); err != nil {
			return err
		}
		return queue(ctx, tx, EventUpdated, inc.Updated, inc, channels, state)
	})
	if err != nil {
		return fmt.Errorf("update incident %d: %w", inc.ID, err)
	}
	return nil
}

// CloseIncident closes an open incident at inc.Closed with inc.CloseCause
// and inc.Resolved, and queues the close for each of channels. A non-nil
// state is saved in the same transaction.
func (db *DB) CloseIncident(ctx context.Context, inc Incident, channels []string, state *TargetState) error {
	err := db.inTx(ctx, func(tx *sql.Tx) error {
		if err := changeOne(ctx, tx, `UPDATE incidents SET closed_ms = ?, close_cause = ?, resolved = ?
			WHERE id = ? AND closed_ms IS NULL`, inc.Closed.UnixMilli(), inc.CloseCause, inc.Resolved, inc.ID); err != nil {
			return err
		}
		return queue(ctx, tx, EventClosed, inc.Closed, inc, channels, state)
	})
	if err != nil {
		return fmt.Errorf("close incident %d: %w", inc.ID, err)
	}
	return nil
}

// inTx runs fn in a transaction, committed only if fn succeeds.
func (db *DB) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// changeOne runs an update of one open incident; none matching is an error.
func changeOne(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return ErrNoOpenIncident
	}
	return nil
}

// queue adds one outbox row per channel, due at once, with a snapshot of
// the incident as the event left it, and saves state when it is not nil.
func queue(ctx context.Context, tx *sql.Tx, event string, at time.Time, inc Incident, channels []string, state *TargetState) error {
	if state != nil {
		if err := saveState(ctx, tx, *state); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
	}
	snapshot, err := json.Marshal(inc)
	if err != nil {
		return err
	}
	for _, ch := range channels {
		if _, err := tx.ExecContext(ctx, `INSERT INTO notifications (incident_id, event, at_ms, channel, incident, next_ms)
			VALUES (?, ?, ?, ?, ?, ?)`, inc.ID, event, at.UnixMilli(), ch, string(snapshot), at.UnixMilli()); err != nil {
			return fmt.Errorf("queue %s for %s: %w", event, ch, err)
		}
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
