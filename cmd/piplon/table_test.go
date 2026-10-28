package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/piplon-sh/piplon/internal/analyze"
	"github.com/piplon-sh/piplon/internal/probe"
	"github.com/piplon-sh/piplon/internal/store"
)

func TestApply(t *testing.T) {
	start := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	down := analyze.Verdict{State: analyze.Down, Cache: "-", Detail: "turn 1: http 401: authentication_error: invalid x-api-key"}
	tests := []struct {
		name               string
		o                  probe.Outcome
		st                 analyze.Status
		state, cache, note string
	}{
		{"ok", probe.Outcome{}, analyze.Status{State: analyze.OK, Last: analyze.Verdict{State: analyze.OK, Cache: "hit"}}, "ok", "hit", ""},
		{"confirmed", probe.Outcome{}, analyze.Status{State: analyze.Down, Last: down}, "down", "-", down.Detail},
		{"pending", probe.Outcome{}, analyze.Status{State: analyze.OK, Pending: analyze.Down, Count: 1, Last: down}, "ok", "-", "down 1/3 · " + down.Detail},
		{"closing", probe.Outcome{}, analyze.Status{State: analyze.Down, Pending: analyze.OK, Count: 2, Last: analyze.Verdict{State: analyze.OK, Cache: "hit"}}, "down", "hit", "ok 2/3"},
		{"budget", probe.Outcome{Skipped: probe.SkipBudget}, analyze.Status{State: analyze.Errors}, "budget", "-", "daily budget reached"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.o.Start = start
			r := row{State: statePending, Cache: "-"}
			r.apply(tt.o, tt.st)
			if r.State != tt.state || r.Cache != tt.cache || r.Note != tt.note || !r.Last.Equal(start) {
				t.Errorf("got state=%q cache=%q note=%q last=%v, want %q %q %q", r.State, r.Cache, r.Note, r.Last, tt.state, tt.cache, tt.note)
			}
		})
	}
}

func TestOverlapKeepsTheLastState(t *testing.T) {
	r := row{State: "errors", Cache: "miss"}
	r.apply(probe.Outcome{Skipped: probe.SkipOverlap}, analyze.Status{State: analyze.Errors})
	if r.State != "errors" || r.Cache != "miss" || r.Note != "previous probe still running" {
		t.Errorf("got %+v", r)
	}
}

func TestApplyStats(t *testing.T) {
	ms := time.Millisecond
	var r row
	r.applyStats([]store.Sample{
		{ProbeID: "a", TTFT: 965 * ms, ExpectOK: true}, {ProbeID: "a", TTFT: 1100 * ms, ExpectOK: true},
		{ProbeID: "b", TTFT: 2088 * ms, ExpectOK: true}, {ProbeID: "b", Error: "overloaded_error: Overloaded"},
		{ProbeID: "c", Error: "refused: bio"},
		{ProbeID: "d", TTFT: 1300 * ms}, // wrong answer
	})
	if r.Probes != 4 || r.ErrPct != 50 {
		t.Errorf("got %d probes and %.1f %% errors, want 4 and 50 (the refusal is not an error)", r.Probes, r.ErrPct)
	}
	if r.TTFT != 1300*ms {
		t.Errorf("TTFT p50: got %v, want 1.3s", r.TTFT)
	}
}

func TestCacheNeverReadIsNotAMiss(t *testing.T) {
	r := row{Cache: "miss"}
	r.applyStats([]store.Sample{{ProbeID: "a", ExpectOK: true}, {ProbeID: "a", ExpectOK: true}})
	if r.Cache != "n/a" {
		t.Errorf("never cached: got %q, want n/a", r.Cache)
	}
	r = row{Cache: "miss"}
	r.applyStats([]store.Sample{{ProbeID: "a", CacheReadTokens: 2000, ExpectOK: true}, {ProbeID: "b", ExpectOK: true}})
	if r.Cache != "miss" {
		t.Errorf("stopped caching: got %q, want miss", r.Cache)
	}
}

func TestRenderFitsTheWidth(t *testing.T) {
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	rows := []row{
		{Target: "anthropic-via-gateway", State: "errors", Cache: "-", Last: now.Add(-41 * time.Second), TTFT: 2088 * time.Millisecond, ErrPct: 8.3, Probes: 12,
			Note: "turn 1: overloaded_error: Overloaded, the server is temporarily unable to handle the request"},
		{Target: "anthropic-direct", State: "ok", Cache: "hit", Last: now.Add(-3 * time.Second), TTFT: 1240 * time.Millisecond, Probes: 12},
	}
	for _, tt := range []struct {
		width      int
		note, last bool
	}{{140, true, true}, {100, true, true}, {80, false, true}, {60, false, false}} {
		out := render(rows, tt.width, now)
		for _, l := range strings.Split(out, "\n") {
			if w := lipgloss.Width(l); w > tt.width {
				t.Errorf("width %d: line is %d wide: %q", tt.width, w, l)
			}
		}
		head := strings.SplitN(out, "\n", 2)[0]
		if strings.Contains(head, "NOTE") != tt.note || strings.Contains(head, "LAST") != tt.last {
			t.Errorf("width %d: header %q, want NOTE=%v LAST=%v", tt.width, head, tt.note, tt.last)
		}
	}
	if out := render(rows, 100, now); !strings.Contains(out, "…") || !strings.HasPrefix(strings.Split(out, "\n")[1], "anthropic-direct") {
		t.Errorf("want the long note cut with an ellipsis and rows sorted by target:\n%s", out)
	}
}

func TestShort(t *testing.T) {
	for d, want := range map[time.Duration]string{10 * time.Second: "10s", time.Minute: "1m", 90 * time.Second: "1m30s", time.Hour: "1h", 5 * time.Minute: "5m"} {
		if got := short(d); got != want {
			t.Errorf("%v: got %q, want %q", d, got, want)
		}
	}
}

func TestLiveTableRedrawsInPlace(t *testing.T) {
	var out syncBuffer
	l := &liveTable{w: &out, width: func() int { return 80 }}
	l.start(func() string { return "piplon dev · standard-v1 · 1 targets" }, []string{"fake"}, nil)
	now := time.Now()
	l.draw(now)
	if got := out.String(); !strings.Contains(got, "fake") || !strings.Contains(got, statePending) || strings.Contains(got, "\x1b[") {
		t.Fatalf("first frame:\n%q", got)
	}
	l.logger().Error("store insert failed", "err", "disk full")
	l.draw(now)
	got := out.String()
	if !strings.Contains(got, "\x1b[4F\x1b[J") {
		t.Errorf("second frame does not move back over the 4 lines of the first:\n%q", got)
	}
	if !strings.Contains(got, "! ") || !strings.Contains(got, "disk full") {
		t.Errorf("the error is not shown under the table:\n%q", got)
	}
	l.logger().Warn("probe failed")
	if strings.Contains(l.lastErr, "probe failed") {
		t.Error("warnings belong in the table, not on the error line")
	}
}
