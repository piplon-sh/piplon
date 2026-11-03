package store

import (
	"testing"
	"time"
)

func TestTargetStateRoundTripAndReplace(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	first := TargetState{Target: "a", Scenario: "standard-v1", State: "slow", Since: at, Cause: "slower than usual",
		LastState: "ok", LastDetail: "", Last: at.Add(time.Minute)}
	if err := db.SaveTargetState(ctx, first); err != nil {
		t.Fatal(err)
	}
	next := first
	next.State, next.Since, next.Cause, next.LastState, next.Last = "ok", at.Add(2*time.Minute), "", "ok", at.Add(3*time.Minute)
	if err := db.SaveTargetState(ctx, next); err != nil {
		t.Fatal(err)
	}
	got, err := db.TargetStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != next {
		t.Fatalf("got %+v, want only %+v", got, next)
	}
}

func TestIncidentChangesSaveTheStateInTheirTransaction(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	inc := Incident{Target: "a", Scenario: "standard-v1", State: "down", Opened: at}
	state := TargetState{Target: "a", Scenario: "standard-v1", State: "down", Since: at, LastState: "down", Last: at}
	id, err := db.OpenIncident(ctx, inc, nil, &state)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := db.TargetStates(ctx); len(got) != 1 || got[0].IncidentID != id {
		t.Fatalf("after open: got %+v, want the state linked to incident %d", got, id)
	}
	// A failed change saves nothing: a second open incident is refused.
	failed := state
	failed.State = "errors"
	if _, err := db.OpenIncident(ctx, inc, nil, &failed); err == nil {
		t.Fatal("a second open incident was accepted")
	}
	if got, _ := db.TargetStates(ctx); got[0].State != "down" {
		t.Errorf("a failed open changed the state: %+v", got[0])
	}
	inc.ID, inc.Closed, inc.Resolved = id, at.Add(time.Minute), true
	state.State, state.IncidentID = "ok", 0
	if err := db.CloseIncident(ctx, inc, nil, &state); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.TargetStates(ctx); got[0].State != "ok" || got[0].IncidentID != 0 {
		t.Errorf("after close: got %+v", got[0])
	}
}

func TestForgetTargetsKeepsOnlyTheGivenOnes(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()
	for _, name := range []string{"a", "b", "c"} {
		if err := db.SaveTargetState(ctx, TargetState{Target: name, State: "ok", Since: time.Now(), Last: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ForgetTargets(ctx, []string{"a", "c"}); err != nil {
		t.Fatal(err)
	}
	got, _ := db.TargetStates(ctx)
	if len(got) != 2 || got[0].Target != "a" || got[1].Target != "c" {
		t.Errorf("got %+v, want a and c", got)
	}
	if err := db.ForgetTargets(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.TargetStates(ctx); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}
