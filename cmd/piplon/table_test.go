package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/piplon-sh/piplon/internal/probe"
	"github.com/piplon-sh/piplon/internal/store"
)

func okTurn(cacheRead int) probe.Turn {
	return probe.Turn{ExpectOK: true, CacheReadTokens: cacheRead}
}

func TestApplyTick(t *testing.T) {
	start := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name               string
		o                  probe.Outcome
		state, cache, note string
	}{
		{"all good reads the cache", probe.Outcome{Turns: []probe.Turn{okTurn(0), okTurn(2000), okTurn(2000)}}, "ok", "hit", ""},
		{"turn 3 misses the cache", probe.Outcome{Turns: []probe.Turn{okTurn(0), okTurn(2000), okTurn(0)}}, "ok", "miss", ""},
		{"refusal on turn 1", probe.Outcome{Turns: []probe.Turn{{Error: "refused: bio", HTTPStatus: 200}}}, "refused", "-", "turn 1: refused: bio"},
		{"model gone", probe.Outcome{Turns: []probe.Turn{{Error: "not_found_error: model: m", HTTPStatus: 404}}}, "gone", "-", "turn 1: not_found_error: model: m"},
		{"overloaded on turn 2", probe.Outcome{Turns: []probe.Turn{okTurn(0), {Error: "overloaded_error: Overloaded", HTTPStatus: 529}}}, "errors", "-", "turn 2: overloaded_error: Overloaded"},
		{"wrong answer", probe.Outcome{Turns: []probe.Turn{okTurn(0), {CacheReadTokens: 2000}, okTurn(2000)}}, "errors", "hit", "turn 2: answer missed its expectation"},
		{"budget", probe.Outcome{Skipped: probe.SkipBudget}, "budget", "-", "daily budget reached"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.o.Start = start
			r := row{State: statePending, Cache: "-"}
			r.applyTick(tt.o, 2)
			if r.State != tt.state || r.Cache != tt.cache || r.Note != tt.note || !r.Last.Equal(start) {
				t.Errorf("got state=%q cache=%q note=%q last=%v, want %q %q %q", r.State, r.Cache, r.Note, r.Last, tt.state, tt.cache, tt.note)
			}
		})
	}
}

func TestOverlapKeepsTheLastState(t *testing.T) {
	r := row{State: "errors", Cache: "miss"}
	r.applyTick(probe.Outcome{Skipped: probe.SkipOverlap}, 2)
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
	l.start(func() string { return "piplon dev · standard-v1 · 1 targets" }, []string{"fake"}, nil, 2)
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
