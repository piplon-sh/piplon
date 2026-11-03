package store

import (
	"context"
	"database/sql"
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

// Due returns up to limit pending notifications whose next attempt is at or
// before now, oldest first. A notification is pending until it is delivered
// or abandoned, and is never due while an earlier one of the same incident
// and channel is pending, so a channel gets an incident's events in order.
func (db *DB) Due(ctx context.Context, now time.Time, limit int) ([]Notification, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT n.id, n.event, n.at_ms, n.channel, n.incident, n.attempts, n.next_ms, n.last_error
		FROM notifications n
		WHERE n.delivered_ms IS NULL AND n.abandoned_ms IS NULL AND n.next_ms <= ?
		AND NOT EXISTS (SELECT 1 FROM notifications e WHERE e.incident_id = n.incident_id AND e.channel = n.channel
			AND e.id < n.id AND e.delivered_ms IS NULL AND e.abandoned_ms IS NULL)
		ORDER BY n.id LIMIT ?`, now.UnixMilli(), limit)
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

// Abandon gives up on a pending notification at at, keeping why. It is
// never due again, and the next one of its incident and channel can go.
func (db *DB) Abandon(ctx context.Context, id int64, at time.Time, reason string) error {
	if _, err := db.sql.ExecContext(ctx, `UPDATE notifications SET abandoned_ms = ?, last_error = ?
		WHERE id = ? AND delivered_ms IS NULL`, at.UnixMilli(), reason, id); err != nil {
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

// ChannelOutbox counts one channel's notifications by status. OldestPending
// is when the oldest pending one was queued, zero when none is pending.
type ChannelOutbox struct {
	Channel                       string
	Pending, Delivered, Abandoned int
	OldestPending                 time.Time
}

// Outbox returns the counts of every channel that has notifications, by
// channel name.
func (db *DB) Outbox(ctx context.Context) ([]ChannelOutbox, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT channel,
		COUNT(*) FILTER (WHERE delivered_ms IS NULL AND abandoned_ms IS NULL),
		COUNT(delivered_ms), COUNT(abandoned_ms),
		MIN(at_ms) FILTER (WHERE delivered_ms IS NULL AND abandoned_ms IS NULL)
		FROM notifications GROUP BY channel ORDER BY channel`)
	if err != nil {
		return nil, fmt.Errorf("query outbox: %w", err)
	}
	defer func() { _ = rows.Close() }() // errors surface through rows.Err
	var out []ChannelOutbox
	for rows.Next() {
		var c ChannelOutbox
		var oldest sql.NullInt64
		if err := rows.Scan(&c.Channel, &c.Pending, &c.Delivered, &c.Abandoned, &oldest); err != nil {
			return nil, fmt.Errorf("query outbox: %w", err)
		}
		if oldest.Valid {
			c.OldestPending = time.UnixMilli(oldest.Int64).UTC()
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query outbox: %w", err)
	}
	return out, nil
}
