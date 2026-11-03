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
	channels  []string // notified of every incident event, through the store's outbox

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

// New returns an Analyzer learning baselines of scenario from db. Every
// incident event is queued for each of channels in the same transaction as
// the incident change.
func New(db store.Store, scenario string, cacheFrom int, channels []string) *Analyzer {
	return &Analyzer{db: db, scenario: scenario, cacheFrom: cacheFrom, channels: channels, targets: map[string]*target{}}
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
	t.status.observe(Classify(o, base, learned, a.cacheFrom), o.Start)
	return t.status, errors.Join(err, t.track(ctx, a, o))
}

// track opens an incident once the target has been out of ok for Confirm
// probes in a row, including from its first probe, follows its state while
// open, and closes it when the target is back to ok. It saves the target's
// state with every probe, in the same transaction as the incident change
// when there is one. A store error leaves the incident as it was; the next
// probe tries again.
func (t *target) track(ctx context.Context, a *Analyzer, o probe.Outcome) error {
	st, inc := &t.status, t.incident
	state := t.state(o, a.scenario)
	switch {
	case inc == nil && st.State != OK && st.Streak >= Confirm:
		next := store.Incident{Target: o.Target, Scenario: a.scenario, State: string(st.State),
			Cause: st.Last.Cause, Detail: st.Last.Detail, Opened: o.Start, Updated: o.Start}
		id, err := a.db.OpenIncident(ctx, next, a.channels, &state)
		if err != nil {
			return fmt.Errorf("analyze %s: %w", o.Target, err)
		}
		next.ID = id
		t.incident, st.Event = &next, Opened
	case inc != nil && st.State == OK:
		closed := *inc
		closed.Closed, closed.CloseCause, closed.Resolved = o.Start, closeRecovered, true
		state.IncidentID = 0
		if err := a.db.CloseIncident(ctx, closed, a.channels, &state); err != nil {
			return fmt.Errorf("analyze %s: %w", o.Target, err)
		}
		t.incident, st.Event, st.Incident = nil, Closed, closed
		return nil
	case inc != nil && string(st.State) != inc.State:
		next := *inc
		next.State, next.Cause, next.Detail, next.Updated = string(st.State), st.Last.Cause, st.Last.Detail, o.Start
		if err := a.db.UpdateIncident(ctx, next, a.channels, &state); err != nil {
			return fmt.Errorf("analyze %s: %w", o.Target, err)
		}
		*inc, st.Event = next, Updated
	default:
		if err := a.db.SaveTargetState(ctx, state); err != nil {
			return fmt.Errorf("analyze %s: %w", o.Target, err)
		}
	}
	st.Incident = store.Incident{}
	if t.incident != nil {
		st.Incident = *t.incident
	}
	return nil
}

// state is what the store keeps of the target after probe o. Its
// IncidentID is the open incident before o; OpenIncident sets the new one.
func (t *target) state(o probe.Outcome, scenario string) store.TargetState {
	st := t.status
	s := store.TargetState{Target: o.Target, Scenario: scenario, State: string(st.State), Since: st.Since,
		Cause: st.Cause, LastState: string(st.Last.State), LastDetail: st.Last.Detail, Last: o.Start}
	if t.incident != nil {
		s.IncidentID = t.incident.ID
	}
	return s
}

// Restore resumes what the store kept, so a restart neither resets a
// target's state nor opens a second incident. A configured target with a
// stored state starts in it, Since included; one without starts from its
// first probe, as on a first run. An open incident of a configured target
// resumes, and with no stored state its state is the incident's. Other
// targets' incidents are closed at now as administrative closes, never
// resolved, and returned; their states are forgotten.
func (a *Analyzer) Restore(ctx context.Context, targets []string, now time.Time) ([]store.Incident, error) {
	open, err := a.db.OpenIncidents(ctx)
	if err != nil {
		return nil, fmt.Errorf("analyze: %w", err)
	}
	states, err := a.db.TargetStates(ctx)
	if err != nil {
		return nil, fmt.Errorf("analyze: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range states {
		if slices.Contains(targets, s.Target) {
			// Streak restarts at 0: an incident still needs Confirm probes
			// in a row to open after a restart.
			a.targets[s.Target] = &target{status: Status{State: State(s.State), Since: s.Since, Cause: s.Cause}}
		}
	}
	var closed []store.Incident
	for _, inc := range open {
		if slices.Contains(targets, inc.Target) {
			t := a.targets[inc.Target]
			if t == nil {
				t = &target{status: Status{State: State(inc.State), Since: inc.Opened, Cause: inc.Cause}}
				a.targets[inc.Target] = t
			}
			t.incident, t.status.Streak, t.status.Incident = &inc, Confirm, inc
			continue
		}
		inc.Closed, inc.CloseCause = now, closeRemoved
		if err := a.db.CloseIncident(ctx, inc, a.channels, nil); err != nil {
			return closed, fmt.Errorf("analyze: %w", err)
		}
		closed = append(closed, inc)
	}
	if err := a.db.ForgetTargets(ctx, targets); err != nil {
		return closed, fmt.Errorf("analyze: %w", err)
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
