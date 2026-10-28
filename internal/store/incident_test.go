package store

import (
	"errors"
	"testing"
	"time"
)

func TestIncidentLifecycle(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()
	opened := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	inc := Incident{Target: "a", Scenario: "standard-v1", State: "down", Cause: "the key was rejected", Detail: "turn 1: http 401", Opened: opened}
	id, err := db.OpenIncident(ctx, inc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.OpenIncident(ctx, inc); err == nil {
		t.Error("a second open incident for the same target was accepted")
	}

	inc.ID, inc.State, inc.Cause, inc.Updated = id, "errors", "the provider is overloaded", opened.Add(time.Minute)
	if err := db.UpdateIncident(ctx, inc); err != nil {
		t.Fatal(err)
	}
	open, err := db.OpenIncidents(ctx)
	if err != nil || len(open) != 1 || open[0].State != "errors" || !open[0].Opened.Equal(opened) || !open[0].Updated.Equal(inc.Updated) {
		t.Fatalf("got %+v, %v", open, err)
	}

	if err := db.CloseIncident(ctx, id, opened.Add(time.Hour), "back to ok", true); err != nil {
		t.Fatal(err)
	}
	if err := db.CloseIncident(ctx, id, opened.Add(time.Hour), "back to ok", true); !errors.Is(err, ErrNoOpenIncident) {
		t.Errorf("closing twice: got %v, want ErrNoOpenIncident", err)
	}
	if err := db.UpdateIncident(ctx, inc); !errors.Is(err, ErrNoOpenIncident) {
		t.Errorf("updating a closed incident: got %v, want ErrNoOpenIncident", err)
	}
	got, err := db.incident(ctx, id)
	if err != nil || !got.Closed.Equal(opened.Add(time.Hour)) || got.CloseCause != "back to ok" || !got.Resolved {
		t.Errorf("closed incident: got %+v, %v", got, err)
	}
	if open, _ := db.OpenIncidents(ctx); len(open) != 0 {
		t.Errorf("still open: %+v", open)
	}
	if _, err := db.OpenIncident(ctx, inc); err != nil {
		t.Errorf("a new incident after the close was refused: %v", err)
	}
}

func TestAdministrativeCloseIsNotResolved(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()
	id, err := db.OpenIncident(ctx, Incident{Target: "old", Scenario: "standard-v1", State: "down", Opened: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CloseIncident(ctx, id, time.Now(), "target removed from config", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.incident(ctx, id); got.Resolved || got.CloseCause != "target removed from config" || got.Closed.IsZero() {
		t.Errorf("got %+v", got)
	}
}
