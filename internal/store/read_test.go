package store

import (
	"testing"
	"time"
)

// probeOf is one probe of target at at: turns samples sharing a ProbeID.
func probeOf(target string, at time.Time, turns int) []Sample {
	out := make([]Sample, turns)
	for i := range out {
		out[i] = sample(target, at, i+1)
		out[i].ProbeID = target + "-" + at.Format(time.RFC3339)
	}
	return out
}

func TestSamplesAfterFollowsNewProbes(t *testing.T) {
	db, _ := openTemp(t)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	if err := db.Insert(t.Context(), probeOf("a", at, 3)...); err != nil {
		t.Fatal(err)
	}
	first, err := db.SamplesAfter(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 {
		t.Fatalf("got %d samples, want 3", len(first))
	}
	// An earlier start inserted later is still new: IDs, not times, order it.
	if err := db.Insert(t.Context(), probeOf("b", at.Add(-time.Minute), 2)...); err != nil {
		t.Fatal(err)
	}
	next, err := db.SamplesAfter(t.Context(), first[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 2 || next[0].Target != "b" || next[0].ID <= first[2].ID {
		t.Fatalf("got %+v, want b's 2 turns after ID %d", next, first[2].ID)
	}
}

func TestLatestProbesKeepsEveryTurnOfEachTargetsLastProbe(t *testing.T) {
	db, _ := openTemp(t)
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	for _, p := range [][]Sample{
		probeOf("a", at, 3), probeOf("b", at, 3),
		probeOf("a", at.Add(time.Minute), 1), // a's last probe stopped at turn 1
	} {
		if err := db.Insert(t.Context(), p...); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.LatestProbes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d samples, want b's 3 turns and a's 1", len(got))
	}
	if got[0].Target != "b" || got[3].Target != "a" || !got[3].Time.Equal(at.Add(time.Minute)) {
		t.Errorf("got %+v", got)
	}
}

func TestOutboxCountsEachChannelByStatus(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()
	opened := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	inc := Incident{Target: "a", Scenario: "standard-v1", State: "down", Opened: opened}
	var err error
	if inc.ID, err = db.OpenIncident(ctx, inc, channels); err != nil {
		t.Fatal(err)
	}
	inc.State, inc.Updated = "errors", opened.Add(time.Minute)
	if err := db.UpdateIncident(ctx, inc, channels); err != nil {
		t.Fatal(err)
	}
	due, err := db.Due(ctx, opened.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	// slack's opening is delivered and webhook's abandoned, so both updates wait.
	for _, n := range due {
		if n.Channel == "slack" {
			err = db.Delivered(ctx, n.ID, opened)
		} else {
			err = db.Abandon(ctx, n.ID, opened, "gone")
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.Outbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []ChannelOutbox{
		{Channel: "slack", Pending: 1, Delivered: 1, OldestPending: opened.Add(time.Minute)},
		{Channel: "webhook", Pending: 1, Abandoned: 1, OldestPending: opened.Add(time.Minute)},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %+v, want %+v", got[i], want[i])
		}
	}
}

func TestPingFailsOnceClosed(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err := db.Ping(t.Context()); err == nil {
		t.Error("ping succeeded on a closed store")
	}
}
