package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "piplon.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, path
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestOpenMigratesAndIsIdempotent(t *testing.T) {
	db, path := openTemp(t)
	want := len(migrationFiles(t))
	if got := userVersion(t, db.sql); got != want {
		t.Fatalf("user_version: got %d, want %d", got, want)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = again.Close() })
	if got := userVersion(t, again.sql); got != want {
		t.Fatalf("user_version after reopen: got %d, want %d", got, want)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	db, path := openTemp(t)
	if _, err := db.sql.ExecContext(context.Background(), "PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	_, err := Open(context.Background(), path)
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("got %v, want ErrSchemaTooNew", err)
	}
}

func TestOpenBadPathNamesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "piplon.db")
	_, err := Open(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("got %v, want an error naming %s", err, path)
	}
}

func migrationFiles(t *testing.T) []string {
	t.Helper()
	files, err := migrationNames()
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestMigrationsAreNumberedContiguously(t *testing.T) {
	for i, name := range migrationFiles(t) {
		want := strconv.Itoa(i+1) + "_"
		if !strings.HasPrefix(strings.TrimLeft(name, "0"), want) {
			t.Errorf("migration %d is %q, want a %04d_ prefix", i, name, i+1)
		}
	}
}

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func sample(target string, at time.Time, turn int) Sample {
	return Sample{
		ProbeID:          "p-" + at.Format(time.RFC3339),
		Time:             at,
		Target:           target,
		Scenario:         "standard-v1",
		Turn:             turn,
		TTFT:             412*time.Millisecond + 7*time.Microsecond,
		Total:            903 * time.Millisecond,
		InputTokens:      2011,
		OutputTokens:     3,
		HTTPStatus:       200,
		CacheReadTokens:  1900,
		CacheWriteTokens: 0,
		ExpectOK:         true,
	}
}

func TestInsertAndQueryRoundTrip(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	ok := sample("anthropic-direct", t0, 2)
	failed := Sample{
		ProbeID: "p-fail", Time: t0.Add(time.Second), Target: "anthropic-direct",
		Scenario: "standard-v1", Turn: 1, Total: 2 * time.Second, HTTPStatus: 529,
		ProviderError: "overloaded_error", Error: "provider overloaded", StreamCut: true,
	}
	if err := db.Insert(ctx, ok, failed); err != nil {
		t.Fatal(err)
	}
	got, err := db.Samples(ctx, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d samples, want 2", len(got))
	}
	for i, want := range []Sample{ok, failed} {
		if got[i].ID == 0 {
			t.Errorf("sample %d: ID not set", i)
		}
		got[i].ID = 0
		if got[i] != want {
			t.Errorf("sample %d:\n got %+v\nwant %+v", i, got[i], want)
		}
	}
}

func TestTTFTZeroIsStoredAsNull(t *testing.T) {
	db, _ := openTemp(t)
	s := sample("t", t0, 1)
	s.TTFT = 0
	if err := db.Insert(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.sql.QueryRowContext(context.Background(), "SELECT count(*) FROM samples WHERE ttft_us IS NULL").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("got %d NULL ttft rows, want 1", n)
	}
}

func TestQueryFilters(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	var all []Sample
	for i := range 4 {
		at := t0.Add(time.Duration(i) * time.Minute)
		all = append(all, sample("a", at, 1), sample("b", at, 1))
	}
	if err := db.Insert(ctx, all...); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		q     Query
		count int
	}{
		{"everything", Query{}, 8},
		{"one target", Query{Target: "a"}, 4},
		{"since is inclusive", Query{Since: t0.Add(2 * time.Minute)}, 4},
		{"until is exclusive", Query{Until: t0.Add(2 * time.Minute)}, 4},
		{"window and target", Query{Target: "b", Since: t0.Add(time.Minute), Until: t0.Add(3 * time.Minute)}, 2},
		{"limit", Query{Limit: 3}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := db.Samples(ctx, tt.q)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.count {
				t.Errorf("got %d samples, want %d", len(got), tt.count)
			}
			for i := 1; i < len(got); i++ {
				if got[i].Time.Before(got[i-1].Time) {
					t.Errorf("samples not ordered by time at index %d", i)
				}
			}
		})
	}
}

func TestInsertIsAtomic(t *testing.T) {
	db, _ := openTemp(t)
	bad := sample("t", t0, 0) // turn must be >= 1
	if err := db.Insert(context.Background(), sample("t", t0, 1), bad); err == nil {
		t.Fatal("expected an error for turn 0")
	}
	got, err := db.Samples(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d samples after a failed batch, want 0", len(got))
	}
}

func TestInsertHonorsCancellation(t *testing.T) {
	db, _ := openTemp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.Insert(ctx, sample("t", t0, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestPrune(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	if err := db.Insert(ctx, sample("t", t0, 1), sample("t", t0.Add(time.Hour), 1)); err != nil {
		t.Fatal(err)
	}
	n, err := db.Prune(ctx, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1 (the cutoff itself is kept)", n)
	}
	left, _ := db.Samples(ctx, Query{})
	if len(left) != 1 || !left[0].Time.Equal(t0.Add(time.Hour)) {
		t.Errorf("unexpected samples left: %+v", left)
	}
}

func TestRunRetentionPrunesThenStops(t *testing.T) {
	db, _ := openTemp(t)
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	old, recent := sample("t", now.Add(-100*24*time.Hour), 1), sample("t", now, 1)
	if err := db.Insert(ctx, old, recent); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		db.RunRetention(ctx, 90*24*time.Hour, time.Hour, nil)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := db.Samples(context.Background(), Query{})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("old sample not pruned; %d samples left", len(got))
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunRetention did not stop after cancel")
	}
}
