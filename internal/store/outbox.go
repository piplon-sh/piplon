package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Notification is one incident event waiting for, or past, delivery to one
// channel.
type Notification struct {
	ID        int64
	Event     string // EventOpened, EventUpdated or EventClosed
	At        time.Time
	Channel   string
	Incident  Incident // as the event left it, not as it is now
	Attempts  int
	Next      time.Time // earliest next attempt
	LastError string
}

// Due returns up to limit undelivered notifications whose next attempt is at
// or before now, oldest first.
func (db *DB) Due(ctx context.Context, now time.Time, limit int) ([]Notification, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT id, event, at_ms, channel, incident, attempts, next_ms, last_error
		FROM notifications WHERE delivered_ms IS NULL AND next_ms <= ? ORDER BY id LIMIT ?`, now.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("query notifications: %w", err)
	}
	defer func() { _ = rows.Close() }() // errors surface through rows.Err
	var out []Notification
	for rows.Next() {
		var n Notification
		var at, next int64
		var snapshot []byte
		if err := rows.Scan(&n.ID, &n.Event, &at, &n.Channel, &snapshot, &n.Attempts, &next, &n.LastError); err != nil {
			return nil, fmt.Errorf("query notifications: %w", err)
		}
		if err := json.Unmarshal(snapshot, &n.Incident); err != nil {
			return nil, fmt.Errorf("notification %d: %w", n.ID, err)
		}
		n.At, n.Next = time.UnixMilli(at).UTC(), time.UnixMilli(next).UTC()
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query notifications: %w", err)
	}
	return out, nil
}

// Delivered marks a notification sent at at; it is never due again.
func (db *DB) Delivered(ctx context.Context, id int64, at time.Time) error {
	if _, err := db.sql.ExecContext(ctx, `UPDATE notifications SET delivered_ms = ?, attempts = attempts + 1
		WHERE id = ?`, at.UnixMilli(), id); err != nil {
		return fmt.Errorf("notification %d: %w", id, err)
	}
	return nil
}

// Failed records a failed attempt and when to try again.
func (db *DB) Failed(ctx context.Context, id int64, next time.Time, reason string) error {
	if _, err := db.sql.ExecContext(ctx, `UPDATE notifications SET attempts = attempts + 1, next_ms = ?, last_error = ?
		WHERE id = ? AND delivered_ms IS NULL`, next.UnixMilli(), reason, id); err != nil {
		return fmt.Errorf("notification %d: %w", id, err)
	}
	return nil
}
