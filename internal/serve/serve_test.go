package serve

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

var t0 = time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)

func openStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "piplon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// turn is one completed turn that met its expectation; probes edit it.
func turn(target string, at time.Time, n int) store.Sample {
	s := store.Sample{
		ProbeID: target + "-" + at.Format(time.RFC3339), Time: at.Add(time.Duration(n-1) * time.Second),
		Target: target, Scenario: "standard-v1", Turn: n,
		TTFT: 400 * time.Millisecond, Total: 900 * time.Millisecond,
		InputTokens: 2000, OutputTokens: 3, HTTPStatus: 200, ExpectOK: true,
	}
	if n == 1 {
		s.CacheWriteTokens = 1900
	} else {
		s.InputTokens, s.CacheReadTokens = 100, 1900
	}
	return s
}

func healthy(target string, at time.Time) []store.Sample {
	return []store.Sample{turn(target, at, 1), turn(target, at, 2), turn(target, at, 3)}
}

func insert(t *testing.T, db store.Store, probes ...[]store.Sample) {
	t.Helper()
	for _, p := range probes {
		if err := db.Insert(t.Context(), p...); err != nil {
			t.Fatal(err)
		}
	}
}

func options(now *time.Time) Options {
	cheap := config.Price{Input: 1, Output: 2, CacheRead: 0.1, CacheWrite: 1}
	return Options{
		Targets: []config.Target{
			{Name: "a", Price: &cheap},
			{Name: "b", Price: &config.DefaultPrice},
		},
		DailyUSD: 5, CacheFrom: 2, Version: "test",
		Log: slog.New(slog.DiscardHandler),
		Now: func() time.Time { return *now },
	}
}

func handler(t *testing.T, db store.Store, now *time.Time) http.Handler {
	t.Helper()
	h, err := Handler(t.Context(), db, options(now))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := getRecorder(t, h, path)
	return rec.Code, rec.Body.String()
}

func getRecorder(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return rec
}

// piplonLines keeps the piplon_ series and their HELP and TYPE lines, which
// leaves out the Go runtime and process ones.
func piplonLines(t *testing.T, body string) string {
	t.Helper()
	var out strings.Builder
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimPrefix(strings.TrimPrefix(line, "# HELP "), "# TYPE "), "piplon_") {
			out.WriteString(line + "\n")
		}
	}
	return out.String()
}

func TestMetricsGolden(t *testing.T) {
	db := openStore(t)
	now := t0.Add(5 * time.Minute)
	// Before start: counted in neither counters nor histograms, but in the
	// day's spend and, for configured targets, in the last-probe gauges.
	insert(t, db, healthy("a", t0.Add(-time.Hour)), healthy("removed", t0.Add(-time.Hour)))
	inc := store.Incident{Target: "b", Scenario: "standard-v1", State: "down", Cause: "the key was rejected", Opened: t0.Add(-10 * time.Minute)}
	if _, err := db.OpenIncident(t.Context(), inc, []string{"slack", "webhook"}, nil); err != nil {
		t.Fatal(err)
	}
	due, err := db.Due(t.Context(), now, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range due {
		if n.Channel == "slack" {
			if err := db.Delivered(t.Context(), n.ID, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, ts := range []store.TargetState{
		{Target: "a", Scenario: "standard-v1", State: "ok", Since: t0.Add(-time.Hour), LastState: "errors", Last: t0.Add(4 * time.Minute)},
		{Target: "b", Scenario: "standard-v1", State: "down", Since: t0.Add(-12 * time.Minute), Cause: "the key was rejected", LastState: "down", Last: t0.Add(3 * time.Minute)},
		{Target: "removed", Scenario: "standard-v1", State: "down", Since: t0.Add(-time.Hour), LastState: "down", Last: t0.Add(-time.Hour)},
	} {
		if err := db.SaveTargetState(t.Context(), ts); err != nil {
			t.Fatal(err)
		}
	}
	h := handler(t, db, &now)

	rejected := []store.Sample{turn("b", t0.Add(time.Minute), 1)}
	rejected[0].HTTPStatus, rejected[0].Error, rejected[0].TTFT, rejected[0].OutputTokens = 401, "HTTP 401: invalid x-api-key", 0, 0
	unreachable := []store.Sample{turn("b", t0.Add(3*time.Minute), 1)}
	unreachable[0].HTTPStatus, unreachable[0].Error, unreachable[0].TTFT, unreachable[0].Total = 0, "dial tcp: i/o timeout", 0, 30*time.Second
	refused := []store.Sample{turn("a", t0.Add(2*time.Minute), 1), turn("a", t0.Add(2*time.Minute), 2)}
	refused[1].Error, refused[1].ExpectOK, refused[1].CacheReadTokens = "refused: bio", false, 0
	cut := healthy("a", t0.Add(4*time.Minute))
	cut[2].StreamCut, cut[2].Error, cut[2].ExpectOK = true, "stream cut before message_stop", false
	insert(t, db, healthy("a", t0), rejected, refused, unreachable, cut)

	code, body := get(t, h, "/metrics")
	if code != http.StatusOK {
		t.Fatalf("got %d: %s", code, body)
	}
	got := piplonLines(t, body)
	path := filepath.Join("testdata", "metrics.golden.txt")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // a fixed test file
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("metrics differ from %s (run with -update to accept):\n%s", path, got)
	}
}

func TestCountersStartAtZeroAndFollowNewProbes(t *testing.T) {
	db := openStore(t)
	now := t0
	insert(t, db, healthy("a", t0.Add(-2*time.Minute)), healthy("a", t0.Add(-time.Minute)))
	h := handler(t, db, &now)
	if _, body := get(t, h, "/metrics"); strings.Contains(body, "piplon_probes_total{") {
		t.Fatalf("probes stored before start were counted:\n%s", piplonLines(t, body))
	}
	insert(t, db, healthy("a", t0))
	_, body := get(t, h, "/metrics")
	if !strings.Contains(body, `piplon_probes_total{result="ok",scenario="standard-v1",target="a"} 1`) {
		t.Errorf("the new probe was not counted once:\n%s", piplonLines(t, body))
	}
	// A second scrape with nothing new counts nothing more.
	if _, again := get(t, h, "/metrics"); !strings.Contains(again, `piplon_probes_total{result="ok",scenario="standard-v1",target="a"} 1`) {
		t.Errorf("a scrape with no new probe changed the counter:\n%s", piplonLines(t, again))
	}
}

func TestSpentResetsAtMidnightUTC(t *testing.T) {
	db := openStore(t)
	now := time.Date(2026, 11, 3, 23, 59, 0, 0, time.UTC)
	insert(t, db, healthy("b", now.Add(-time.Minute)))
	h := handler(t, db, &now)
	if _, body := get(t, h, "/metrics"); strings.Contains(body, "piplon_budget_spent_usd 0\n") {
		t.Fatal("today's probe was not priced at start")
	}
	now = now.Add(2 * time.Minute)
	if _, body := get(t, h, "/metrics"); !strings.Contains(body, "piplon_budget_spent_usd 0\n") {
		t.Errorf("spent did not reset at 00:00 UTC:\n%s", piplonLines(t, body))
	}
}

func TestHealthAndReadiness(t *testing.T) {
	db := openStore(t)
	now := t0
	h := handler(t, db, &now)
	for _, path := range []string{"/healthz", "/readyz"} {
		if code, body := get(t, h, path); code != http.StatusOK || body != "ok\n" {
			t.Errorf("%s: got %d %q, want 200 ok", path, code, body)
		}
	}
	_ = db.Close()
	if code, _ := get(t, h, "/readyz"); code != http.StatusServiceUnavailable {
		t.Errorf("/readyz on a closed store: got %d, want 503", code)
	}
	if code, _ := get(t, h, "/metrics"); code != http.StatusServiceUnavailable {
		t.Errorf("/metrics on a closed store: got %d, want 503", code)
	}
	if code, _ := get(t, h, "/healthz"); code != http.StatusOK {
		t.Errorf("/healthz on a closed store: got %d, want 200", code)
	}
}

func TestHandlerFailsOnAnUnreadableStore(t *testing.T) {
	db := openStore(t)
	_ = db.Close()
	now := t0
	if _, err := Handler(t.Context(), db, options(&now)); err == nil {
		t.Error("Handler accepted a closed store")
	}
}

func TestServeStopsWhenCancelled(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "hi") }))
	}()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String()+"/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not stop within 3 s")
	}
	if _, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ln.Addr().String()); err == nil {
		t.Error("the listener is still open")
	}
}

func TestServeReportsAClosedListener(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	if err := Serve(t.Context(), ln, http.NotFoundHandler()); err == nil || errors.Is(err, http.ErrServerClosed) {
		t.Errorf("got %v, want the listener's error", err)
	}
}
