package probe

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/scenario"
)

// Job is one target to probe on its own interval.
type Job struct {
	Target   string
	Interval time.Duration
	Price    config.Price
	Prober   Prober
}

// Scheduler probes every Job until its context is cancelled. See the package
// documentation for the exact rules.
type Scheduler struct {
	Scenario    *scenario.Scenario
	Jobs        []Job
	Timeout     time.Duration
	Jitter      time.Duration
	Concurrency int
	Budget      *Budget
	Report      func(Outcome) // called from many goroutines
}

func (s *Scheduler) check() error {
	switch {
	case s.Scenario == nil:
		return fmt.Errorf("%w: no scenario", ErrSetup)
	case len(s.Jobs) == 0:
		return fmt.Errorf("%w: no jobs", ErrSetup)
	case s.Concurrency < 1:
		return fmt.Errorf("%w: concurrency %d", ErrSetup, s.Concurrency)
	case s.Budget == nil:
		return fmt.Errorf("%w: no budget", ErrSetup)
	case s.Report == nil:
		return fmt.Errorf("%w: no report function", ErrSetup)
	}
	for _, j := range s.Jobs {
		if j.Interval <= 0 || j.Prober == nil {
			return fmt.Errorf("%w: job %s needs an interval and a prober", ErrSetup, j.Target)
		}
	}
	return nil
}

// Run blocks until ctx is cancelled and every in-flight probe has returned.
// It returns an error only when the Scheduler is not set up correctly.
func (s *Scheduler) Run(ctx context.Context) error {
	if err := s.check(); err != nil {
		return err
	}
	slots := make(chan struct{}, s.Concurrency)
	var wg sync.WaitGroup
	for _, j := range s.Jobs {
		wg.Go(func() { s.loop(ctx, j, slots, &wg) })
	}
	wg.Wait()
	return nil
}

// loop fires j at start and on every tick, skipping ticks that overlap.
func (s *Scheduler) loop(ctx context.Context, j Job, slots chan struct{}, wg *sync.WaitGroup) {
	ticker := time.NewTicker(j.Interval)
	defer ticker.Stop()
	var pending atomic.Bool
	for {
		tick := time.Now()
		if pending.CompareAndSwap(false, true) {
			wg.Go(func() {
				defer pending.Store(false)
				s.once(ctx, j, tick, slots)
			})
		} else {
			s.Report(Outcome{Target: j.Target, Scenario: s.Scenario.ID(), Start: tick, Skipped: SkipOverlap})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// once runs a single probe: jitter, slot, budget, probe, report.
func (s *Scheduler) once(ctx context.Context, j Job, tick time.Time, slots chan struct{}) {
	if s.Jitter > 0 && !sleep(ctx, rand.N(s.Jitter)) { //nolint:gosec // spreading load needs no crypto randomness
		return
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return
	}

	o := Outcome{
		Target:   j.Target,
		ProbeID:  fmt.Sprintf("%s-%d", j.Target, tick.UnixNano()),
		Scenario: s.Scenario.ID(),
		Start:    tick,
	}
	reserved := estimate(j.Price, s.Scenario)
	if !s.Budget.Reserve(reserved) {
		o.ProbeID, o.Skipped = "", SkipBudget
		s.Report(o)
		return
	}

	pctx, cancel := context.WithTimeout(ctx, s.Timeout)
	res := j.Prober.Probe(pctx, s.Scenario)
	cancel()
	o.Turns = res.Turns
	if len(o.Turns) == 0 {
		o.Turns = []Turn{{Start: time.Now(), Error: "prober returned no turns"}}
	}
	o.CostUSD = cost(j.Price, o.Turns)
	if missingUsage(o.Turns) {
		o.CostUSD = max(o.CostUSD, reserved) // unknown usage is charged the high estimate
	}
	s.Budget.Settle(reserved, o.CostUSD)
	s.Report(o)
}

// missingUsage reports whether a turn completed without reporting any token,
// as servers that ignore stream_options.include_usage do.
func missingUsage(turns []Turn) bool {
	for _, t := range turns {
		if t.Error == "" && t.InputTokens+t.OutputTokens+t.CacheReadTokens+t.CacheWriteTokens == 0 {
			return true
		}
	}
	return false
}

// sleep waits d or until ctx is done, and reports whether d fully elapsed.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
