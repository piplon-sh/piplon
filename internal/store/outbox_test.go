package store

import (
	"testing"
	"time"
)

var channels = []string{"slack", "webhook"}

func TestIncidentEventsAreQueuedPerChannel(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()
	opened := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	inc := Incident{Target: "a", Scenario: "standard-v1", State: "down", Cause: "the key was rejected", Opened: opened}
	var err error
	if inc.ID, err = db.OpenIncident(ctx, inc, channels); err != nil {
		t.Fatal(err)
	}
	inc.State, inc.Updated = "errors", opened.Add(time.Minute)
	if err := db.UpdateIncident(ctx, inc, channels); err != nil {
		t.Fatal(err)
	}
	inc.Closed, inc.CloseCause, inc.Resolved = opened.Add(time.Hour), "back to ok", true
	if err := db.CloseIncident(ctx, inc, channels); err != nil {
		t.Fatal(err)
	}

	due, err := db.Due(ctx, opened.Add(2*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ event, channel, state string }{
		{EventOpened, "slack", "down"}, {EventOpened, "webhook", "down"},
		{EventUpdated, "slack", "errors"}, {EventUpdated, "webhook", "errors"},
		{EventClosed, "slack", "errors"}, {EventClosed, "webhook", "errors"},
	}
	if len(due) != len(want) {
		t.Fatalf("got %d notifications, want %d", len(due), len(want))
	}
	for i, w := range want {
		n := due[i]
		if n.Event != w.event || n.Channel != w.channel || n.Incident.State != w.state || n.Incident.ID != inc.ID {
			t.Errorf("notification %d: got %s %s %s, want %s %s %s", i, n.Event, n.Channel, n.Incident.State, w.event, w.channel, w.state)
		}
	}
	if opening := due[0]; !opening.At.Equal(opened) || !opening.Incident.Closed.IsZero() {
		t.Errorf("the opening must keep the incident as it was then: %+v", opening)
	}
	if closing := due[5]; !closing.Incident.Resolved || closing.Incident.CloseCause != "back to ok" {
		t.Errorf("closing: %+v", closing.Incident)
	}
}

func TestAFailedChangeQueuesNothing(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()
	inc := Incident{ID: 42, Target: "a", State: "errors", Updated: time.Now(), Closed: time.Now()}
	if err := db.UpdateIncident(ctx, inc, channels); err == nil {
		t.Fatal("updated an incident that does not exist")
	}
	if err := db.CloseIncident(ctx, inc, channels); err == nil {
		t.Fatal("closed an incident that does not exist")
	}
	if due, _ := db.Due(ctx, time.Now().Add(time.Hour), 10); len(due) != 0 {
		t.Errorf("queued for a change that did not happen: %+v", due)
	}
}

func TestDeliveryAndRetry(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	if _, err := db.OpenIncident(ctx, Incident{Target: "a", State: "down", Opened: now}, channels); err != nil {
		t.Fatal(err)
	}
	due, _ := db.Due(ctx, now, 10)
	if len(due) != 2 {
		t.Fatalf("got %d due, want 2", len(due))
	}
	if err := db.Delivered(ctx, due[0].ID, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Failed(ctx, due[1].ID, now.Add(time.Minute), "http 503"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Due(ctx, now.Add(59*time.Second), 10); len(got) != 0 {
		t.Errorf("due before its next attempt: %+v", got)
	}
	got, _ := db.Due(ctx, now.Add(time.Minute), 10)
	if len(got) != 1 || got[0].Channel != "webhook" || got[0].Attempts != 1 || got[0].LastError != "http 503" {
		t.Errorf("after a failure: got %+v, want the webhook retry only", got)
	}
	if got, _ := db.Due(ctx, now.Add(time.Minute), 1); len(got) != 1 {
		t.Errorf("limit: got %d", len(got))
	}
}
