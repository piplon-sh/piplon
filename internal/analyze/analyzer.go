package analyze

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/piplon-sh/piplon/internal/probe"
	"github.com/piplon-sh/piplon/internal/store"
)

// relearnEvery is how long a baseline is used before it is learned again.
const relearnEvery = time.Hour

// Analyzer keeps the state of every target it observes.
type Analyzer struct {
	db        store.Store
	scenario  string
	cacheFrom int

	mu      sync.Mutex
	targets map[string]*target
}

type target struct {
	mu       sync.Mutex
	baseline Baseline
	learned  time.Time // zero until the first successful learn
	status   Status
	incident *store.Incident // the open one, nil when none
}

// Event is what a probe did to a target's incident.
type Event string

// Incident events.
const (
	Opened  Event = "opened"
	Updated Event = "updated" // the state changed while open
	Closed  Event = "closed"  // back to ok; administrative closes come from Restore
)

// Close causes. Only closeRecovered is a resolution.
const (
	closeRecovered = "back to ok for 3 probes in a row"
	closeRemoved   = "target removed from config"
)

// New returns an Analyzer learning baselines of scenario from db.
func New(db store.Store, scenario string, cacheFrom int) *Analyzer {
	return &Analyzer{db: db, scenario: scenario, cacheFrom: cacheFrom, targets: map[string]*target{}}
}

// Observe applies one outcome and returns the target's status. A skipped
// tick leaves the status as it was. The baseline is learned on the first
// probe and again once relearnEvery has passed; if the store cannot be read,
// the previous baseline is kept, the error returned, and the next probe tries
// again.
func (a *Analyzer) Observe(ctx context.Context, o probe.Outcome) (Status, error) {
	a.mu.Lock()
	t := a.targets[o.Target]
	if t == nil {
		t = &target{}
		a.targets[o.Target] = t
	}
	a.mu.Unlock()

	t.mu.Lock()
	defer t.mu.Unlock()
	t.status.Changed, t.status.Event = false, ""
	if o.Skipped != "" || len(o.Turns) == 0 {
		return t.status, nil
	}
	var err error
	if t.learned.IsZero() || o.Start.Sub(t.learned) >= relearnEvery {
		err = t.learn(ctx, a, o)
	}
	base, learned := t.baseline.At(o.Start)
	t.status.observe(Classify(o, base, learned, a.cacheFrom))
	return t.status, errors.Join(err, t.track(ctx, a, o))
}

// track opens an incident once the target has been out of ok for Confirm
// probes in a row, including from its first probe, follows its state while
// open, and closes it when the target is back to ok. A store error leaves
// things as they were; the next probe tries again.
func (t *target) track(ctx context.Context, a *Analyzer, o probe.Outcome) error {
	st, inc := &t.status, t.incident
	switch {
	case inc == nil && st.State != OK && st.Streak >= Confirm:
		next := store.Incident{Target: o.Target, Scenario: a.scenario, State: string(st.State),
			Cause: st.Last.Cause, Detail: st.Last.Detail, Opened: o.Start, Updated: o.Start}
		id, err := a.db.OpenIncident(ctx, next)
		if err != nil {
			return fmt.Errorf("analyze %s: %w", o.Target, err)
		}
		next.ID = id
		t.incident, st.Event = &next, Opened
	case inc != nil && st.State == OK:
		if err := a.db.CloseIncident(ctx, inc.ID, o.Start, closeRecovered, true); err != nil {
			return fmt.Errorf("analyze %s: %w", o.Target, err)
		}
		inc.Closed, inc.CloseCause, inc.Resolved = o.Start, closeRecovered, true
		t.incident, st.Event, st.Incident = nil, Closed, *inc
		return nil
	case inc != nil && string(st.State) != inc.State:
		next := *inc
		next.State, next.Cause, next.Detail, next.Updated = string(st.State), st.Last.Cause, st.Last.Detail, o.Start
		if err := a.db.UpdateIncident(ctx, next); err != nil {
			return fmt.Errorf("analyze %s: %w", o.Target, err)
		}
		*inc, st.Event = next, Updated
	}
	st.Incident = store.Incident{}
	if t.incident != nil {
		st.Incident = *t.incident
	}
	return nil
}

// Restore loads the open incidents. One of a target in targets resumes: the
// target starts in the incident's state, so a restart neither opens a second
// incident nor closes it before Confirm probes are back to ok. The others are
// closed at now as administrative closes, never resolved, and returned.
func (a *Analyzer) Restore(ctx context.Context, targets []string, now time.Time) ([]store.Incident, error) {
	open, err := a.db.OpenIncidents(ctx)
	if err != nil {
		return nil, fmt.Errorf("analyze: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var closed []store.Incident
	for _, inc := range open {
		if slices.Contains(targets, inc.Target) {
			a.targets[inc.Target] = &target{incident: &inc,
				status: Status{State: State(inc.State), Streak: Confirm, Incident: inc}}
			continue
		}
		if err := a.db.CloseIncident(ctx, inc.ID, now, closeRemoved, false); err != nil {
			return closed, fmt.Errorf("analyze: %w", err)
		}
		inc.Closed, inc.CloseCause = now, closeRemoved
		closed = append(closed, inc)
	}
	return closed, nil
}

// Confirming reports whether target's latest probe failed hard and still
// needs confirming; see Status.Confirming.
func (a *Analyzer) Confirming(target string) bool {
	a.mu.Lock()
	t := a.targets[target]
	a.mu.Unlock()
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status.Confirming()
}

// learn reads the target's samples before this probe, so it is not part of
// its own baseline.
func (t *target) learn(ctx context.Context, a *Analyzer, o probe.Outcome) error {
	samples, err := a.db.Samples(ctx, store.Query{Target: o.Target, Since: o.Start.Add(-Window), Until: o.Start})
	if err != nil {
		return fmt.Errorf("analyze %s: %w", o.Target, err)
	}
	t.baseline, t.learned = Learn(samples, a.scenario, a.cacheFrom, o.Start), o.Start
	return nil
}
