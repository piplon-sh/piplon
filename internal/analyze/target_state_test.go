package analyze

import (
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/store"
)

func TestAStateDatesFromTheFirstProbeThatConfirmedIt(t *testing.T) {
	var s Status
	minute := func(n int) time.Time { return monday.Add(time.Duration(n) * time.Minute) }
	s.observe(Verdict{State: OK}, minute(0))
	s.observe(Verdict{State: Slow, Cause: causeSlow}, minute(1))
	s.observe(Verdict{State: OK}, minute(2)) // breaks the run
	for n := 3; n < 3+Confirm; n++ {
		s.observe(Verdict{State: Slow, Cause: causeSlow}, minute(n))
	}
	if s.State != Slow || !s.Since.Equal(minute(3)) || s.Cause != causeSlow {
		t.Errorf("got %s since %v cause %q, want slow since %v", s.State, s.Since, s.Cause, minute(3))
	}
}

// stateOf reads target's stored state.
func stateOf(t *testing.T, db *store.DB, target string) (store.TargetState, bool) {
	t.Helper()
	states, err := db.TargetStates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range states {
		if s.Target == target {
			return s, true
		}
	}
	return store.TargetState{}, false
}

func TestTheStoredStateFollowsEveryProbe(t *testing.T) {
	db := openStore(t)
	a := New(db, "standard-v1", 2, nil)
	ok := outcome(turn(1000*ms, 0), turn(900*ms, 4000), turn(900*ms, 4000))
	var incident int64
	for i, step := range []struct {
		bad        bool // a bad key, else an ok probe
		state, was State
		since      int
		open       bool
	}{
		{true, Down, Down, 0, false}, // down at once
		{true, Down, Down, 0, false},
		{true, Down, Down, 0, true}, // the incident opens
		{false, Down, OK, 0, true},  // not confirmed yet
		{false, Down, OK, 0, true},
		{false, OK, OK, 3, false}, // back to ok since its first ok probe
	} {
		o := ok
		if step.bad {
			o = outcome(badKey)
		}
		st, err := a.Observe(t.Context(), at(o, i))
		if err != nil {
			t.Fatal(err)
		}
		got, found := stateOf(t, db, "t")
		if !found {
			t.Fatalf("probe %d: no state stored", i+1)
		}
		wantSince := monday.Add(time.Duration(step.since) * time.Minute)
		if got.State != string(step.state) || got.LastState != string(step.was) || !got.Since.Equal(wantSince) ||
			!got.Last.Equal(monday.Add(time.Duration(i)*time.Minute)) {
			t.Errorf("probe %d: got %+v, want %s since %v, latest %s", i+1, got, step.state, wantSince, step.was)
		}
		if step.open {
			incident = st.Incident.ID
		}
		if wantID := map[bool]int64{true: incident, false: 0}[step.open]; got.IncidentID != wantID {
			t.Errorf("probe %d: incident %d, want %d", i+1, got.IncidentID, wantID)
		}
		wantCause := ""
		if step.state == Down {
			wantCause = cause(Down, badKey)
		}
		if got.Cause != wantCause {
			t.Errorf("probe %d: cause %q, want %q", i+1, got.Cause, wantCause)
		}
	}
	if incident == 0 {
		t.Fatal("no incident opened")
	}
}

func TestRestoreResumesStoredStatesAndForgetsRemovedTargets(t *testing.T) {
	db := openStore(t)
	since := monday.Add(-time.Hour)
	for _, target := range []string{"t", "old"} {
		err := db.SaveTargetState(t.Context(), store.TargetState{Target: target, Scenario: "standard-v1", State: "slow",
			Since: since, Cause: causeSlow, LastState: "slow", Last: monday})
		if err != nil {
			t.Fatal(err)
		}
	}
	a := New(db, "standard-v1", 2, nil)
	if _, err := a.Restore(t.Context(), []string{"t"}, monday); err != nil {
		t.Fatal(err)
	}
	if _, found := stateOf(t, db, "old"); found {
		t.Error("the state of a removed target was kept")
	}
	ok := outcome(turn(1000*ms, 0), turn(900*ms, 4000), turn(900*ms, 4000))
	st, err := a.Observe(t.Context(), at(ok, 1))
	if err != nil {
		t.Fatal(err)
	}
	if st.State != Slow || !st.Since.Equal(since) || st.Pending != OK || st.Changed {
		t.Errorf("after restart: got %+v, want slow resumed, ok pending", st)
	}
	for i := 2; i <= Confirm; i++ {
		if st, _ = a.Observe(t.Context(), at(ok, i)); st.Event != "" {
			t.Errorf("probe %d: event %q, want none: slow opened no incident", i, st.Event)
		}
	}
	if st.State != OK || !st.Changed {
		t.Errorf("after %d ok: got %+v, want back to ok", Confirm, st)
	}
}

func TestRestoreWaitsForConfirmBeforeOpeningAfterARestart(t *testing.T) {
	db := openStore(t)
	// The first probe set down at once; piplon stopped before an incident.
	err := db.SaveTargetState(t.Context(), store.TargetState{Target: "t", Scenario: "standard-v1", State: "down",
		Since: monday, Cause: cause(Down, badKey), LastState: "down", Last: monday})
	if err != nil {
		t.Fatal(err)
	}
	a := New(db, "standard-v1", 2, nil)
	if _, err := a.Restore(t.Context(), []string{"t"}, monday); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= Confirm; i++ {
		st, _ := a.Observe(t.Context(), at(outcome(badKey), i))
		if want := map[bool]Event{true: Opened, false: ""}[i == Confirm]; st.Event != want {
			t.Fatalf("probe %d after restart: event %q, want %q", i, st.Event, want)
		}
	}
}
