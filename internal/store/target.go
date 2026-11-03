package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// TargetState is a target's current state as piplon run judges it: the
// confirmed state and when it began, and what the latest probe measured,
// which may disagree while a new state is not confirmed yet.
type TargetState struct {
	Target     string
	Scenario   string
	State      string // the confirmed analyze state, for example "down"
	Since      time.Time
	Cause      string // the probable cause of State, empty for ok
	LastState  string // the latest probe's verdict
	LastDetail string // what the latest probe measured
	Last       time.Time
	IncidentID int64 // the open incident, 0 when none
}

// SaveTargetState writes a target's state, replacing the previous one.
func (db *DB) SaveTargetState(ctx context.Context, ts TargetState) error {
	if err := saveState(ctx, db.sql, ts); err != nil {
		return fmt.Errorf("save state %s: %w", ts.Target, err)
	}
	return nil
}

// execer is what saveState needs from a *sql.DB or a *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func saveState(ctx context.Context, db execer, ts TargetState) error {
	incident := sql.NullInt64{Int64: ts.IncidentID, Valid: ts.IncidentID != 0}
	_, err := db.ExecContext(ctx, `INSERT INTO target_states
		(target, scenario, state, since_ms, cause, last_state, last_detail, last_ms, incident_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (target) DO UPDATE SET scenario = excluded.scenario, state = excluded.state,
		since_ms = excluded.since_ms, cause = excluded.cause, last_state = excluded.last_state,
		last_detail = excluded.last_detail, last_ms = excluded.last_ms, incident_id = excluded.incident_id`,
		ts.Target, ts.Scenario, ts.State, ts.Since.UnixMilli(), ts.Cause, ts.LastState, ts.LastDetail,
		ts.Last.UnixMilli(), incident)
	return err
}

// TargetStates returns every stored target state, by target name.
func (db *DB) TargetStates(ctx context.Context) ([]TargetState, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT target, scenario, state, since_ms, cause,
		last_state, last_detail, last_ms, incident_id FROM target_states ORDER BY target`)
	if err != nil {
		return nil, fmt.Errorf("query states: %w", err)
	}
	defer func() { _ = rows.Close() }() // errors surface through rows.Err
	var out []TargetState
	for rows.Next() {
		var ts TargetState
		var since, last int64
		var incident sql.NullInt64
		if err := rows.Scan(&ts.Target, &ts.Scenario, &ts.State, &since, &ts.Cause,
			&ts.LastState, &ts.LastDetail, &last, &incident); err != nil {
			return nil, fmt.Errorf("query states: %w", err)
		}
		ts.Since, ts.Last, ts.IncidentID = time.UnixMilli(since).UTC(), time.UnixMilli(last).UTC(), incident.Int64
		out = append(out, ts)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query states: %w", err)
	}
	return out, nil
}

// ForgetTargets deletes the states of every target not in keep.
func (db *DB) ForgetTargets(ctx context.Context, keep []string) error {
	query, args := "DELETE FROM target_states", make([]any, len(keep))
	if len(keep) > 0 {
		// Only placeholders are joined; every name is a bound parameter.
		query += " WHERE target NOT IN (?" + strings.Repeat(", ?", len(keep)-1) + ")" //nolint:gosec // see above
		for i, k := range keep {
			args[i] = k
		}
	}
	if _, err := db.sql.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("forget targets: %w", err)
	}
	return nil
}
