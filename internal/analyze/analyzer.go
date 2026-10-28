package analyze

import (
	"context"
	"fmt"
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
}

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
	if o.Skipped != "" || len(o.Turns) == 0 {
		t.status.Changed = false
		return t.status, nil
	}
	var err error
	if t.learned.IsZero() || o.Start.Sub(t.learned) >= relearnEvery {
		err = t.learn(ctx, a, o)
	}
	base, learned := t.baseline.At(o.Start)
	t.status.observe(Classify(o, base, learned, a.cacheFrom))
	return t.status, err
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
