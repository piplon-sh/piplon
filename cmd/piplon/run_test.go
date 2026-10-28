package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/probe"
	"github.com/piplon-sh/piplon/internal/store"
)

// syncBuffer is a log sink the test can read while run writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeAnthropic answers the three standard-v1 turns from the probe fixtures
// and counts the requests it receives.
func fakeAnthropic(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		turn := n.Add(1)
		data, err := os.ReadFile(filepath.Join("..", "..", "internal", "probe", "testdata", "anthropic", fmt.Sprintf("turn%d.sse", turn)))
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &n
}

// writeConfig writes a one-target config and returns its path and store path.
func writeConfig(t *testing.T, format, url string, dailyUSD float64) (string, string) {
	t.Helper()
	t.Setenv("PIPLON_TEST_KEY", "sk-test-key")
	dir := t.TempDir()
	db := filepath.Join(dir, "piplon.db")
	cfg := fmt.Sprintf(`interval: 10s
jitter: 0s
timeout: 5s
budget: {daily_usd: %v}
targets:
  - name: fake
    format: %s
    url: %s
    region: eu-west-1
    model: m
    key: ${PIPLON_TEST_KEY}
store: {path: %s}
`, dailyUSD, format, url, db)
	if format == "bedrock" {
		cfg = strings.Replace(cfg, "    key: ${PIPLON_TEST_KEY}\n", "", 1)
	}
	path := filepath.Join(dir, "piplon.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, db
}

// startRun runs the loop in the background; stop cancels it and returns its
// error, failing the test if shutdown takes longer than 5 s.
func startRun(t *testing.T, path string, logs *syncBuffer) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, path, slog.New(slog.NewJSONHandler(logs, nil)), nil) }()
	return func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("run did not stop within 5 s")
			return nil
		}
	}
}

// waitFor polls cond until it holds or 5 s pass.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRunProbesStoresAndStops(t *testing.T) {
	url, _ := fakeAnthropic(t)
	path, dbPath := writeConfig(t, "anthropic", url, 5)
	var logs syncBuffer
	stop := startRun(t, path, &logs)
	waitFor(t, "the first probe to finish", func() bool { return strings.Contains(logs.String(), "probe done") })
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	samples, err := db.Samples(t.Context(), store.Query{Target: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 3 {
		t.Fatalf("got %d samples, want the 3 turns of standard-v1", len(samples))
	}
	for _, s := range samples {
		if !s.ExpectOK || s.Scenario != "standard-v1" {
			t.Errorf("turn %d: expect_ok=%v scenario=%q", s.Turn, s.ExpectOK, s.Scenario)
		}
	}
	if strings.Contains(logs.String(), "sk-test-key") {
		t.Error("the key leaked into the logs")
	}
}

func TestRunShowsTheLiveTable(t *testing.T) {
	url, _ := fakeAnthropic(t)
	path, _ := writeConfig(t, "anthropic", url, 5)
	var screen syncBuffer
	view := &liveTable{w: &screen, width: func() int { return 100 }}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, path, view.logger(), view) }()
	waitFor(t, "the table to show the first probe", func() bool {
		return strings.Contains(screen.String(), "hit") // turns 2 and 3 read the cache
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := screen.String()
	for _, want := range []string{"standard-v1 · 1 targets · every 10s", "TARGET", "fake", "ok"} {
		if !strings.Contains(got, want) {
			t.Errorf("screen is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "probe done") {
		t.Error("logs leaked onto the table")
	}
}

func TestRunSeedsTheBudgetFromToday(t *testing.T) {
	url, requests := fakeAnthropic(t)
	path, dbPath := writeConfig(t, "anthropic", url, 0.01)
	db, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// 10 000 uncached input tokens at the default 3 USD/M already spend 0.03.
	err = db.Insert(t.Context(), store.Sample{ProbeID: "p", Time: time.Now(), Target: "fake", Scenario: "standard-v1", Turn: 1, InputTokens: 10_000})
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}

	var logs syncBuffer
	stop := startRun(t, path, &logs)
	waitFor(t, "a budget skip", func() bool { return strings.Contains(logs.String(), `"reason":"budget"`) })
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("sent %d requests over an exhausted budget", n)
	}
}

func TestRunRejectsUnsupportedFormatsBeforeOpeningTheStore(t *testing.T) {
	path, dbPath := writeConfig(t, "bedrock", "", 5)
	err := run(t.Context(), path, slog.New(slog.DiscardHandler), nil)
	if !errors.Is(err, probe.ErrUnsupported) || !strings.Contains(err.Error(), "target fake") {
		t.Fatalf("got %v, want ErrUnsupported naming the target", err)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Error("the store was created for a config that cannot run")
	}
}

func TestRunNamesAMissingConfig(t *testing.T) {
	_, err := execute(t, "run", "--config", filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil || !strings.Contains(err.Error(), "nope.yaml") {
		t.Fatalf("got %v, want an error naming the file", err)
	}
}
