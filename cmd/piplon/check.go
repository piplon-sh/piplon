package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/piplon-sh/piplon/internal/analyze"
	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/probe"
	"github.com/piplon-sh/piplon/internal/scenario"
)

// Exit codes of piplon check.
const (
	checkPassed = 0 // every target passed every threshold
	checkFailed = 1 // a threshold was crossed
	checkBroken = 2 // the pass could not be judged: config, flags, interruption
)

// retryDelay is how long a hard failure waits before it is probed again.
var retryDelay = 5 * time.Second

var errInterrupted = errors.New("check interrupted before every target answered")

// checkOptions are the flags of piplon check.
type checkOptions struct {
	config       string
	targets      []string
	maxTTFT      time.Duration
	maxErrors    int
	requireCache bool
	retries      int
	json         bool
}

func newCheckCmd() *cobra.Command {
	var o checkOptions
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Probe every target once and exit 0 (passed), 1 (a threshold crossed) or 2 (not judged)",
		Long: `Probe every target once, or those named by --target, and judge the answers
against the thresholds. Nothing is stored and nothing is notified.

Without a threshold, check fails when a target is refused, errors, down or gone.
Exit codes: 0 every target passed, 1 a threshold was crossed, 2 the pass could
not be judged (config, flags, unsupported format, interruption).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCheck(cmd.Context(), o, cmd.OutOrStdout())
		},
	}
	f := cmd.Flags()
	f.StringVarP(&o.config, "config", "c", "piplon.yaml", "path to the config file")
	f.StringArrayVar(&o.targets, "target", nil, "probe only this target; repeat for more")
	f.DurationVar(&o.maxTTFT, "max-ttft", 0, "fail a target whose TTFT, the median of its turns, is above this")
	f.IntVar(&o.maxErrors, "max-errors", 0, "targets allowed to fail hard (refused, errors, down, gone)")
	f.BoolVar(&o.requireCache, "require-cache", false, "fail a target that does not read the prompt cache")
	f.IntVar(&o.retries, "retries", 0, "probe a target that failed hard again, up to this many times, 5 s apart")
	f.BoolVar(&o.json, "json", false, "write the result as JSON (schema "+checkSchema+")")
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &exitError{code: checkBroken, err: err} })
	return cmd
}

// runCheck runs the pass and writes its result to w. A pass that cannot be
// judged exits 2, with a JSON error document on w under --json.
func runCheck(ctx context.Context, o checkOptions, w io.Writer) error {
	rep, err := check(ctx, o)
	if err != nil {
		if o.json {
			_ = writeCheckJSON(w, checkErrorDoc{Schema: checkSchema, ExitCode: checkBroken, Error: err.Error()})
		}
		return &exitError{code: checkBroken, err: err}
	}
	if o.json {
		err = writeCheckJSON(w, rep.doc())
	} else {
		err = rep.text(w)
	}
	if err != nil {
		return &exitError{code: checkBroken, err: err}
	}
	if code := rep.exitCode(); code != checkPassed {
		return &exitError{code: code}
	}
	return nil
}

// check probes the chosen targets once each, Concurrency at a time, and
// judges them.
func check(ctx context.Context, o checkOptions) (*checkReport, error) {
	switch {
	case o.maxTTFT < 0:
		return nil, errors.New("--max-ttft must not be negative")
	case o.maxErrors < 0:
		return nil, errors.New("--max-errors must not be negative")
	case o.retries < 0:
		return nil, errors.New("--retries must not be negative")
	}
	cfg, err := config.LoadTargets(o.config, o.targets)
	if err != nil {
		return nil, err
	}
	sc, err := scenario.Load(cfg.Scenario)
	if err != nil {
		return nil, err
	}
	probers := make([]probe.Prober, len(cfg.Targets))
	for i, t := range cfg.Targets {
		if probers[i], err = probe.ForTarget(t); err != nil {
			return nil, err
		}
	}

	rep := &checkReport{opts: o, scenario: sc.ID(), started: time.Now(), results: make([]targetResult, len(cfg.Targets))}
	slots := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	for i, t := range cfg.Targets {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			rep.results[i] = probeOnce(ctx, t, probers[i], sc, cfg.Timeout, o.retries)
		})
	}
	wg.Wait()
	rep.finished = time.Now()
	if ctx.Err() != nil {
		return nil, errInterrupted
	}
	rep.judge()
	return rep, nil
}

// targetResult is one target's last attempt and what all attempts cost.
type targetResult struct {
	target   config.Target
	turns    []probe.Turn
	verdict  analyze.Verdict
	ttft     time.Duration // the probe TTFT, 0 when it failed hard
	attempts int
	costUSD  float64
	failures []string // set by judge
}

// probeOnce probes t under timeout, again after retryDelay while it fails
// hard and retries are left. With no history there is no baseline, so the
// verdict is ok or a hard failure; slow and cache are judged by thresholds.
func probeOnce(ctx context.Context, t config.Target, p probe.Prober, sc *scenario.Scenario, timeout time.Duration,
	retries int) targetResult {
	r := targetResult{target: t}
	for {
		r.attempts++
		pctx, cancel := context.WithTimeout(ctx, timeout)
		start := time.Now()
		res := p.Probe(pctx, sc)
		cancel()
		o := probe.Outcome{Target: t.Name, ProbeID: fmt.Sprintf("check-%s-%d", t.Name, r.attempts),
			Scenario: sc.ID(), Start: start, Turns: res.Turns}
		r.turns, r.costUSD = res.Turns, r.costUSD+probe.SamplesCost(*t.Price, o.Samples())
		r.verdict = analyze.Classify(o, analyze.Stats{}, false, sc.Cache.ExpectHitFromTurn)
		r.ttft = 0
		if !r.verdict.State.Hard() {
			r.ttft = analyze.TTFT(res.Turns)
			return r
		}
		if r.attempts > retries {
			return r
		}
		select {
		case <-time.After(retryDelay):
		case <-ctx.Done():
			return r
		}
	}
}
