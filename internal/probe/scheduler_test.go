package probe

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/scenario"
)

// fakeProber takes `took` of (fake) time per probe and records concurrency.
type fakeProber struct {
	took     time.Duration
	tokens   int
	calls    atomic.Int32
	running  atomic.Int32
	maxSeen  atomic.Int32
	deadline atomic.Value // time.Time of the last probe's ctx deadline
}

func (f *fakeProber) Probe(ctx context.Context, _ *scenario.Scenario) Result {
	f.calls.Add(1)
	n := f.running.Add(1)
	defer f.running.Add(-1)
	for {
		m := f.maxSeen.Load()
		if n <= m || f.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	if d, ok := ctx.Deadline(); ok {
		f.deadline.Store(d)
	}
	start := time.Now()
	select {
	case <-time.After(f.took):
		return Result{Turns: []Turn{{Start: start, Total: f.took, InputTokens: f.tokens, HTTPStatus: 200, ExpectOK: true}}}
	case <-ctx.Done():
		return Result{Turns: []Turn{{Start: start, Total: time.Since(start), Error: ctx.Err().Error()}}}
	}
}

// recorder collects outcomes from many goroutines.
type recorder struct {
	mu  sync.Mutex
	got []Outcome
}

func (r *recorder) report(o Outcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, o)
}

func (r *recorder) outcomes() []Outcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Outcome(nil), r.got...)
}

func (r *recorder) count(skip SkipReason) int {
	n := 0
	for _, o := range r.outcomes() {
		if o.Skipped == skip {
			n++
		}
	}
	return n
}

func loadScenario(t *testing.T) *scenario.Scenario {
	t.Helper()
	s, err := scenario.Load("standard-v1")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newScheduler(t *testing.T, r *recorder, jobs ...Job) *Scheduler {
	return &Scheduler{
		Scenario:    loadScenario(t),
		Jobs:        jobs,
		Timeout:     30 * time.Second,
		Concurrency: 4,
		Budget:      NewBudget(1000),
		Report:      r.report,
	}
}

// runFor runs s until d of fake time has passed, then cancels and waits.
func runFor(t *testing.T, s *Scheduler, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(d)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestProbesAtStartThenEveryInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		p := &fakeProber{took: time.Second}
		start := time.Now()
		runFor(t, newScheduler(t, r, Job{Target: "a", Interval: time.Minute, Price: config.DefaultPrice, Prober: p}), 150*time.Second)
		got := r.outcomes()
		if len(got) != 3 {
			t.Fatalf("got %d outcomes in 150s at 60s interval, want 3 (t=0, 60, 120)", len(got))
		}
		for i, o := range got {
			if want := start.Add(time.Duration(i) * time.Minute); !o.Start.Equal(want) {
				t.Errorf("probe %d started at %v, want %v", i, o.Start.Sub(start), want.Sub(start))
			}
			if o.Target != "a" || o.Scenario != "standard-v1" || o.ProbeID == "" || len(o.Turns) != 1 {
				t.Errorf("probe %d: incomplete outcome %+v", i, o)
			}
		}
	})
}

func TestJitterDelaysWithinRange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		s := newScheduler(t, r, Job{Target: "a", Interval: time.Minute, Price: config.DefaultPrice, Prober: &fakeProber{took: time.Second}})
		s.Jitter = 5 * time.Second
		start := time.Now()
		runFor(t, s, 10*time.Minute-time.Second)
		got := r.outcomes()
		if len(got) != 10 {
			t.Fatalf("got %d outcomes, want 10", len(got))
		}
		for i, o := range got {
			tick := start.Add(time.Duration(i) * time.Minute)
			if delay := o.Turns[0].Start.Sub(tick); delay < 0 || delay >= 5*time.Second {
				t.Errorf("probe %d: jitter delay %v outside [0, 5s)", i, delay)
			}
		}
	})
}

func TestConcurrencyLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		p := &fakeProber{took: 10 * time.Second}
		var jobs []Job
		for _, name := range []string{"a", "b", "c", "d", "e"} {
			jobs = append(jobs, Job{Target: name, Interval: time.Minute, Price: config.DefaultPrice, Prober: p})
		}
		s := newScheduler(t, r, jobs...)
		s.Concurrency = 2
		runFor(t, s, 59*time.Second)
		if got := p.maxSeen.Load(); got != 2 {
			t.Errorf("max concurrent probes: got %d, want 2", got)
		}
		if got := p.calls.Load(); got != 5 {
			t.Errorf("calls: got %d, want 5 (all queued probes eventually run)", got)
		}
	})
}

func TestOverlappingTickIsSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		p := &fakeProber{took: 25 * time.Second}
		s := newScheduler(t, r, Job{Target: "a", Interval: 10 * time.Second, Price: config.DefaultPrice, Prober: p})
		s.Timeout = time.Minute
		runFor(t, s, 29*time.Second) // ticks at 0 (runs until 25), 10 and 20 overlap
		if got := r.count(SkipOverlap); got != 2 {
			t.Errorf("overlap skips: got %d, want 2", got)
		}
		if got := p.calls.Load(); got != 1 {
			t.Errorf("calls: got %d, want 1", got)
		}
	})
}

func TestTimeoutBoundsEachProbe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		p := &fakeProber{took: time.Hour}
		start := time.Now()
		runFor(t, newScheduler(t, r, Job{Target: "a", Interval: time.Minute, Price: config.DefaultPrice, Prober: p}), 59*time.Second)
		if d, _ := p.deadline.Load().(time.Time); !d.Equal(start.Add(30 * time.Second)) {
			t.Errorf("probe deadline: got %v after start, want 30s", d.Sub(start))
		}
		got := r.outcomes()
		if len(got) != 1 || got[0].Turns[0].Error == "" {
			t.Fatalf("want one outcome carrying the timeout error, got %+v", got)
		}
	})
}

func TestCancelStopsInFlightProbes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		p := &fakeProber{took: time.Hour}
		s := newScheduler(t, r, Job{Target: "a", Interval: time.Minute, Price: config.DefaultPrice, Prober: p})
		s.Timeout = 5 * time.Minute
		start := time.Now()
		runFor(t, s, 10*time.Second)
		if elapsed := time.Since(start); elapsed != 10*time.Second {
			t.Errorf("Run returned %v after start, want immediately at cancel (10s)", elapsed)
		}
		if got := p.running.Load(); got != 0 {
			t.Errorf("%d probes still running after Run returned", got)
		}
	})
}

func TestBudgetStopsProbing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		sc := loadScenario(t)
		price := config.Price{Input: 1, Output: 1}
		// Room for exactly two estimated probes.
		s := newScheduler(t, r, Job{Target: "a", Interval: time.Minute, Price: price, Prober: &fakeProber{took: time.Second, tokens: 1e6}})
		s.Budget = NewBudget(2*estimate(price, sc) + 1e-9)
		runFor(t, s, 5*time.Minute-time.Second)
		if got := r.count(""); got != 1 {
			t.Errorf("probes run: got %d, want 1 (the first actual cost, 1 USD, exceeds two estimates)", got)
		}
		if got := r.count(SkipBudget); got != 4 {
			t.Errorf("budget skips: got %d, want 4", got)
		}
	})
}

func TestRunRejectsBadSetup(t *testing.T) {
	r := &recorder{}
	tests := []struct {
		name string
		edit func(*Scheduler)
	}{
		{"no jobs", func(s *Scheduler) { s.Jobs = nil }},
		{"zero concurrency", func(s *Scheduler) { s.Concurrency = 0 }},
		{"no scenario", func(s *Scheduler) { s.Scenario = nil }},
		{"no budget", func(s *Scheduler) { s.Budget = nil }},
		{"zero interval", func(s *Scheduler) { s.Jobs[0].Interval = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newScheduler(t, r, Job{Target: "a", Interval: time.Minute, Prober: &fakeProber{}})
			tt.edit(s)
			if err := s.Run(t.Context()); !errors.Is(err, ErrSetup) {
				t.Fatalf("got %v, want ErrSetup", err)
			}
		})
	}
}

func TestProbeWithoutUsageIsChargedItsEstimate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		sc := loadScenario(t)
		price := config.Price{Input: 1, Output: 1}
		// The prober reports no tokens at all, like a server that ignores
		// stream_options.include_usage. Room for two estimates.
		s := newScheduler(t, r, Job{Target: "a", Interval: time.Minute, Price: price, Prober: &fakeProber{took: time.Second}})
		s.Budget = NewBudget(2.5 * estimate(price, sc))
		runFor(t, s, 5*time.Minute-time.Second)
		if got := r.count(""); got != 2 {
			t.Errorf("probes run: got %d, want 2 (each charged its estimate)", got)
		}
		for _, o := range r.outcomes() {
			if o.Skipped == "" && !near(o.CostUSD, estimate(price, sc)) {
				t.Errorf("cost: got %v, want the estimate %v", o.CostUSD, estimate(price, sc))
			}
		}
	})
}

// starts returns when each outcome's tick fired, relative to start.
func starts(r *recorder, start time.Time) []time.Duration {
	var out []time.Duration
	for _, o := range r.outcomes() {
		out = append(out, o.Start.Sub(start))
	}
	return out
}

func TestConfirmProbesSoonerThenKeepsTheSchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		s := newScheduler(t, r, Job{Target: "a", Interval: 5 * time.Minute, Price: config.DefaultPrice, Prober: &fakeProber{took: time.Second}})
		var asked int
		s.Confirm = func(Outcome) bool { asked++; return asked < 3 } // the third probe confirms
		start := time.Now()
		runFor(t, s, 5*time.Minute+time.Second)
		want := []time.Duration{0, 31 * time.Second, 62 * time.Second, 5 * time.Minute}
		if got := starts(r, start); !slices.Equal(got, want) {
			t.Errorf("ticks at %v, want %v: 30 s after each probe to confirm, then the regular schedule", got, want)
		}
	})
}

func TestConfirmKeepsAShorterInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		s := newScheduler(t, r, Job{Target: "a", Interval: 20 * time.Second, Price: config.DefaultPrice, Prober: &fakeProber{took: time.Second}})
		s.Confirm = func(Outcome) bool { return true }
		start := time.Now()
		runFor(t, s, 41*time.Second)
		if got, want := starts(r, start), []time.Duration{0, 20 * time.Second, 40 * time.Second}; !slices.Equal(got, want) {
			t.Errorf("ticks at %v, want %v", got, want)
		}
	})
}

func TestBudgetSkipsAreNotConfirmed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &recorder{}
		s := newScheduler(t, r, Job{Target: "a", Interval: 5 * time.Minute, Price: config.DefaultPrice, Prober: &fakeProber{took: time.Second}})
		s.Budget = NewBudget(0)
		var asked atomic.Int32
		s.Confirm = func(Outcome) bool { asked.Add(1); return true }
		runFor(t, s, time.Minute)
		if n := asked.Load(); n != 0 || r.count(SkipBudget) != 1 {
			t.Errorf("Confirm asked %d times for %d budget skips, want 0 for 1", n, r.count(SkipBudget))
		}
	})
}
