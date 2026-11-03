package notify

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/store"
)

// fake records what it is sent; err, when set, fails every send.
type fake struct {
	name  string
	mu    sync.Mutex
	got   []store.Notification
	err   error
	block bool // wait for the context instead of answering
}

func (f *fake) Name() string { return f.name }

func (f *fake) Send(ctx context.Context, n store.Notification) error {
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.got = append(f.got, n)
	return nil
}

func (f *fake) sent() []store.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.Notification(nil), f.got...)
}

func openStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "piplon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// setup opens and closes one incident for channels and returns a dispatcher
// whose clock is *now.
func setup(t *testing.T, channels []string, sinks ...Channel) (*Dispatcher, *store.DB, *time.Time) {
	t.Helper()
	db := openStore(t)
	inc := incident()
	var err error
	if inc.ID, err = db.OpenIncident(t.Context(), inc, channels, nil); err != nil {
		t.Fatal(err)
	}
	inc.Closed, inc.CloseCause, inc.Resolved = opened.Add(10*time.Minute), "back to ok for 3 probes in a row", true
	if err := db.CloseIncident(t.Context(), inc, channels, nil); err != nil {
		t.Fatal(err)
	}
	now := opened.Add(11 * time.Minute)
	d := NewDispatcher(db, slog.New(slog.DiscardHandler), sinks...)
	d.now = func() time.Time { return now }
	return d, db, &now
}

func events(ns []store.Notification) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Event)
	}
	return out
}

func TestDispatcherSendsInOrder(t *testing.T) {
	slack := &fake{name: "slack"}
	d, _, _ := setup(t, []string{"slack"}, slack)
	d.drain(t.Context())
	if got := events(slack.sent()); len(got) != 2 || got[0] != store.EventOpened || got[1] != store.EventClosed {
		t.Errorf("sent %v, want opened then closed", got)
	}
	d.drain(t.Context())
	if n := len(slack.sent()); n != 2 {
		t.Errorf("sent %d after a second drain, want each event once", n)
	}
}

func TestDispatcherRetriesWithBackoff(t *testing.T) {
	slack := &fake{name: "slack", err: errors.New("http 503")}
	d, db, now := setup(t, []string{"slack"}, slack)
	d.drain(t.Context())
	*now = now.Add(29 * time.Second)
	if due, _ := db.Due(t.Context(), *now, 10); len(due) != 0 {
		t.Fatalf("due before its 30 s backoff: %+v", due)
	}
	slack.err = nil
	*now = now.Add(time.Second)
	d.drain(t.Context())
	if got := events(slack.sent()); len(got) != 2 {
		t.Errorf("after the backoff: sent %v, want opened and closed", got)
	}
	for attempts, want := range map[int]time.Duration{0: 30 * time.Second, 1: time.Minute, 5: 16 * time.Minute, 6: 30 * time.Minute, 40: 30 * time.Minute} {
		if got := backoff(attempts); got != want {
			t.Errorf("backoff(%d) = %v, want %v", attempts, got, want)
		}
	}
}

func TestDispatcherAbandonsStaleAndUnknown(t *testing.T) {
	slack := &fake{name: "slack"}
	d, db, now := setup(t, []string{"slack", "removed"}, slack)
	*now = opened.Add(25 * time.Hour)
	d.drain(t.Context())
	if got := slack.sent(); len(got) != 0 {
		t.Errorf("sent stale events: %v", events(got))
	}
	if due, _ := db.Due(t.Context(), now.Add(time.Hour), 10); len(due) != 0 {
		t.Errorf("still pending: %+v", due)
	}
}

func TestShutdownLeavesTheSendPending(t *testing.T) {
	slack := &fake{name: "slack", block: true}
	d, db, now := setup(t, []string{"slack"}, slack)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { d.drain(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not stop with its context")
	}
	due, _ := db.Due(t.Context(), *now, 10)
	if len(due) != 1 || due[0].Event != store.EventOpened || due[0].Attempts != 0 {
		t.Errorf("after shutdown: got %+v, want the opening pending with no attempt counted", due)
	}
}

func TestRunDrainsOnWake(t *testing.T) {
	slack := &fake{name: "slack"}
	d, _, _ := setup(t, []string{"slack"}, slack)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go d.Run(ctx)
	d.Wake()
	for deadline := time.Now().Add(5 * time.Second); len(slack.sent()) < 2; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("nothing sent")
		}
	}
	if got := d.Names(); len(got) != 1 || got[0] != "slack" {
		t.Errorf("Names: %v", got)
	}
}
