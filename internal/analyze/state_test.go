package analyze

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/probe"
	"github.com/piplon-sh/piplon/internal/store"
)

func turn(ttft time.Duration, cacheRead int) probe.Turn {
	return probe.Turn{TTFT: ttft, ExpectOK: true, CacheReadTokens: cacheRead}
}

func outcome(turns ...probe.Turn) probe.Outcome {
	return probe.Outcome{Target: "t", ProbeID: fmt.Sprint(time.Now().UnixNano()), Scenario: "standard-v1", Start: monday, Turns: turns}
}

func TestClassify(t *testing.T) {
	usual := Stats{Probes: 100, TTFT50: 1000 * ms, TTFT95: 1400 * ms, Cached: 100, CacheShare: 0.95}
	never := usual
	never.CacheShare = 0
	tests := []struct {
		name          string
		o             probe.Outcome
		base          Stats
		learned       bool
		state         State
		cache, detail string
	}{
		{"usual", outcome(turn(1000*ms, 0), turn(900*ms, 4000), turn(950*ms, 4000)), usual, true, OK, "hit", ""},
		{"above p95, below 1.5 x p50", outcome(turn(1450*ms, 0), turn(1450*ms, 4000), turn(1450*ms, 4000)), usual, true, OK, "hit", ""},
		{"slow", outcome(turn(1600*ms, 0), turn(1600*ms, 4000), turn(1600*ms, 4000)), usual, true, Slow, "hit", "TTFT 1600 ms, usually 1000 ms"},
		{"one slow turn is not a slow probe", outcome(turn(5000*ms, 0), turn(900*ms, 4000), turn(950*ms, 4000)), usual, true, OK, "hit", ""},
		{"slow needs a baseline", outcome(turn(9000*ms, 0), turn(9000*ms, 4000), turn(9000*ms, 4000)), Stats{}, false, OK, "hit", ""},
		{"cache miss", outcome(turn(1000*ms, 0), turn(900*ms, 4000), turn(950*ms, 0)), usual, true, CacheMiss, "miss", "cache not read, usually read by 95 % of probes"},
		{"never cached is not a miss", outcome(turn(1000*ms, 0), turn(900*ms, 0), turn(950*ms, 0)), never, true, OK, "miss", ""},
		{"cache miss needs a baseline", outcome(turn(1000*ms, 0), turn(900*ms, 0), turn(950*ms, 0)), Stats{}, false, OK, "miss", ""},
		{"refused", outcome(probe.Turn{Error: "refused: bio", HTTPStatus: 200}), usual, true, Refused, "-", "turn 1: refused: bio"},
		{"gone", outcome(probe.Turn{Error: "http 404: not_found_error: model: m", HTTPStatus: 404, ProviderError: "not_found_error"}), usual, true, Gone, "-", "turn 1: http 404: not_found_error: model: m"},
		{"down", outcome(probe.Turn{Error: "http 401: authentication_error: invalid x-api-key", HTTPStatus: 401}), usual, true, Down, "-", "turn 1: http 401: authentication_error: invalid x-api-key"},
		{"errors on turn 2", outcome(turn(1000*ms, 0), probe.Turn{Error: "http 529: overloaded_error: Overloaded", HTTPStatus: 529}), usual, true, Errors, "-", "turn 2: http 529: overloaded_error: Overloaded"},
		{"wrong answer", outcome(turn(1000*ms, 0), probe.Turn{TTFT: 900 * ms, CacheReadTokens: 4000}, turn(950*ms, 4000)), usual, true, Errors, "hit", "turn 2: answer missed its expectation"},
		{"a refusal after a wrong answer is still errors", outcome(probe.Turn{TTFT: 900 * ms}, probe.Turn{Error: "refused"}), usual, true, Errors, "-", "turn 1: answer missed its expectation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Classify(tt.o, tt.base, tt.learned, 2)
			if v.State != tt.state || v.Cache != tt.cache || v.Detail != tt.detail {
				t.Errorf("got %+v, want %s %q %q", v, tt.state, tt.cache, tt.detail)
			}
		})
	}
}

func TestFirstProbeSetsTheState(t *testing.T) {
	for _, first := range []State{OK, Down} {
		var s Status
		s.observe(Verdict{State: first})
		if s.State != first || s.Pending != "" || s.Changed {
			t.Errorf("first probe %s: got %+v, want state %s at once, not reported as a change", first, s, first)
		}
		s.observe(Verdict{State: Errors})
		if s.State != first || s.Pending != Errors || s.Count != 1 {
			t.Errorf("after first probe %s: got %+v, want hysteresis on the next change", first, s)
		}
	}
}

func TestStatusNeedsThreeInARow(t *testing.T) {
	s := Status{State: OK}
	for i, tt := range []struct {
		seen    State
		state   State
		pending State
		changed bool
	}{
		{Down, OK, Down, false},
		{Down, OK, Down, false},
		{OK, OK, "", false}, // one ok resets the count
		{Down, OK, Down, false},
		{Errors, OK, Errors, false}, // another failure starts over
		{Errors, OK, Errors, false},
		{Errors, Errors, "", true},
		{OK, Errors, OK, false}, // closing takes three too
		{OK, Errors, OK, false},
		{OK, OK, "", true},
	} {
		s.observe(Verdict{State: tt.seen})
		if s.State != tt.state || s.Pending != tt.pending || s.Changed != tt.changed {
			t.Fatalf("probe %d (%s): got %+v, want state %s pending %q changed %v", i+1, tt.seen, s, tt.state, tt.pending, tt.changed)
		}
	}
}

func TestHardStates(t *testing.T) {
	for s, hard := range map[State]bool{OK: false, CacheMiss: false, Slow: false, Refused: true, Errors: true, Down: true, Gone: true} {
		if s.Hard() != hard {
			t.Errorf("%s: Hard() = %v", s, !hard)
		}
	}
}

func TestAnalyzerLearnsFromTheStore(t *testing.T) {
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "piplon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	samples := every(30*time.Minute, monday.Add(-2*24*time.Hour), monday, flat)
	for i := range samples {
		samples[i].Target = "t"
	}
	if err := db.Insert(t.Context(), samples...); err != nil {
		t.Fatal(err)
	}

	a := New(db, "standard-v1", 2)
	if st, _ := a.Observe(t.Context(), outcome(turn(1000*ms, 0), turn(1000*ms, 4000), turn(1000*ms, 4000))); st.State != OK {
		t.Fatalf("first probe: got %+v", st)
	}
	slow := outcome(turn(2000*ms, 0), turn(2000*ms, 4000), turn(2000*ms, 4000))
	for i := range Confirm {
		st, err := a.Observe(t.Context(), slow)
		if err != nil {
			t.Fatal(err)
		}
		if st.Last.State != Slow || st.Changed != (i == Confirm-1) {
			t.Fatalf("probe %d: got %+v", i+1, st)
		}
	}
	st, _ := a.Observe(t.Context(), probe.Outcome{Target: "t", Skipped: probe.SkipBudget})
	if st.State != Slow || st.Changed {
		t.Errorf("a skipped tick changed the status: %+v", st)
	}
	if st, _ := a.Observe(t.Context(), probe.Outcome{Target: "new", Start: monday, Turns: slow.Turns}); st.Last.State != OK {
		t.Errorf("a target without history judged slow: %+v", st)
	}
}

// brokenStore fails every read.
type brokenStore struct{ store.Store }

func (brokenStore) Samples(context.Context, store.Query) ([]store.Sample, error) {
	return nil, errors.New("disk I/O error")
}

func TestAnalyzerSurvivesAStoreError(t *testing.T) {
	a := New(brokenStore{}, "standard-v1", 2)
	st, err := a.Observe(t.Context(), outcome(probe.Turn{Error: "timeout"}))
	if err == nil || st.State != Down {
		t.Errorf("got %+v and %v, want the probe judged and the error returned", st, err)
	}
}
