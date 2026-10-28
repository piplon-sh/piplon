package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Sample is one turn of one probe against one target.
type Sample struct {
	ID               int64 // set by the store on read
	ProbeID          string
	Time             time.Time // when the turn started, stored at ms precision in UTC
	Target           string
	Scenario         string // scenario ID, for example "standard-v1"
	Turn             int    // 1-based
	TTFT             time.Duration
	Total            time.Duration
	InputTokens      int
	OutputTokens     int
	HTTPStatus       int // 0 when no HTTP response arrived
	ProviderError    string
	Error            string
	StreamCut        bool
	CacheReadTokens  int
	CacheWriteTokens int
	ExpectOK         bool
}

// Query selects samples. Zero fields do not filter. Since is inclusive and
// Until exclusive; results are ordered by time, then turn.
type Query struct {
	Target string
	Since  time.Time
	Until  time.Time
	Limit  int
}

// Store is what the rest of piplon depends on.
type Store interface {
	Insert(ctx context.Context, samples ...Sample) error
	Samples(ctx context.Context, q Query) ([]Sample, error)
	Prune(ctx context.Context, cutoff time.Time) (int64, error)
	OpenIncident(ctx context.Context, inc Incident) (int64, error)
	UpdateIncident(ctx context.Context, inc Incident) error
	CloseIncident(ctx context.Context, id int64, at time.Time, cause string, resolved bool) error
	OpenIncidents(ctx context.Context) ([]Incident, error)
	Close() error
}

// DB is the SQLite implementation of Store.
type DB struct {
	sql *sql.DB
}

var _ Store = (*DB)(nil)

// Open opens or creates the database at path and migrates it.
func Open(ctx context.Context, path string) (*DB, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store %s: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store %s: %w", path, err)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store %s: %w", path, err)
	}
	return &DB{sql: db}, nil
}

// Close releases the database.
func (db *DB) Close() error { return db.sql.Close() }

const insertSQL = `INSERT INTO samples (
	probe_id, ts_ms, target, scenario, turn, ttft_us, total_us,
	input_tokens, output_tokens, http_status, provider_error, error,
	stream_cut, cache_read_tokens, cache_write_tokens, expect_ok
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// Insert writes all samples in one transaction: all or none.
func (db *DB) Insert(ctx context.Context, samples ...Sample) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("insert: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed
	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return fmt.Errorf("insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for _, s := range samples {
		var ttft sql.NullInt64
		if s.TTFT > 0 {
			ttft = sql.NullInt64{Int64: s.TTFT.Microseconds(), Valid: true}
		}
		_, err := stmt.ExecContext(ctx,
			s.ProbeID, s.Time.UnixMilli(), s.Target, s.Scenario, s.Turn, ttft, s.Total.Microseconds(),
			s.InputTokens, s.OutputTokens, s.HTTPStatus, s.ProviderError, s.Error,
			s.StreamCut, s.CacheReadTokens, s.CacheWriteTokens, s.ExpectOK)
		if err != nil {
			return fmt.Errorf("insert %s turn %d: %w", s.Target, s.Turn, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("insert: %w", err)
	}
	return nil
}

// Samples returns the samples matching q.
func (db *DB) Samples(ctx context.Context, q Query) ([]Sample, error) {
	var where []string
	var args []any
	if q.Target != "" {
		where, args = append(where, "target = ?"), append(args, q.Target)
	}
	if !q.Since.IsZero() {
		where, args = append(where, "ts_ms >= ?"), append(args, q.Since.UnixMilli())
	}
	if !q.Until.IsZero() {
		where, args = append(where, "ts_ms < ?"), append(args, q.Until.UnixMilli())
	}
	query := `SELECT id, probe_id, ts_ms, target, scenario, turn, ttft_us, total_us,
		input_tokens, output_tokens, http_status, provider_error, error,
		stream_cut, cache_read_tokens, cache_write_tokens, expect_ok FROM samples`
	if len(where) > 0 {
		// Only constant fragments are joined; every value is a bound parameter.
		query += " WHERE " + strings.Join(where, " AND ") //nolint:gosec // see above
	}
	query += " ORDER BY ts_ms, turn, id"
	if q.Limit > 0 {
		query, args = query+" LIMIT ?", append(args, q.Limit)
	}

	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query samples: %w", err)
	}
	defer func() { _ = rows.Close() }() // errors surface through rows.Err
	var out []Sample
	for rows.Next() {
		s, err := scanSample(rows)
		if err != nil {
			return nil, fmt.Errorf("query samples: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query samples: %w", err)
	}
	return out, nil
}

func scanSample(rows *sql.Rows) (Sample, error) {
	var s Sample
	var tsMS, totalUS int64
	var ttftUS sql.NullInt64
	err := rows.Scan(&s.ID, &s.ProbeID, &tsMS, &s.Target, &s.Scenario, &s.Turn, &ttftUS, &totalUS,
		&s.InputTokens, &s.OutputTokens, &s.HTTPStatus, &s.ProviderError, &s.Error,
		&s.StreamCut, &s.CacheReadTokens, &s.CacheWriteTokens, &s.ExpectOK)
	s.Time = time.UnixMilli(tsMS).UTC()
	s.TTFT = time.Duration(ttftUS.Int64) * time.Microsecond
	s.Total = time.Duration(totalUS) * time.Microsecond
	return s, err
}

// Prune deletes samples strictly older than cutoff and returns how many.
func (db *DB) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := db.sql.ExecContext(ctx, "DELETE FROM samples WHERE ts_ms < ?", cutoff.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("prune: %w", err)
	}
	return res.RowsAffected()
}

// RunRetention deletes samples older than retention now and then every
// interval, until ctx is cancelled. Failures are logged and retried on the
// next tick; a nil log uses slog.Default.
func (db *DB) RunRetention(ctx context.Context, retention, every time.Duration, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		n, err := db.Prune(ctx, time.Now().Add(-retention))
		switch {
		case err != nil && ctx.Err() == nil:
			log.Error("retention failed", "err", err)
		case n > 0:
			log.Info("retention pruned samples", "count", n, "retention", retention)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
