package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/probe"
	"github.com/piplon-sh/piplon/internal/scenario"
	"github.com/piplon-sh/piplon/internal/store"
)

// retentionEvery is how often old samples are pruned.
const retentionEvery = time.Hour

func newRunCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Probe every configured target on a schedule",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), path, newLogger(cmd.ErrOrStderr()))
		},
	}
	cmd.Flags().StringVarP(&path, "config", "c", "piplon.yaml", "path to the config file")
	return cmd
}

// run probes until ctx is cancelled, storing every outcome.
func run(ctx context.Context, path string, log *slog.Logger) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	sc, err := scenario.Load(cfg.Scenario)
	if err != nil {
		return err
	}
	jobs, err := buildJobs(cfg.Targets)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.Store.Path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	spent, err := spentToday(ctx, db, cfg.Targets)
	if err != nil {
		return err
	}
	budget := probe.NewBudget(cfg.Budget.DailyUSD)
	budget.Seed(spent)

	// Retention must stop before the deferred Close.
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() { db.RunRetention(ctx, time.Duration(cfg.Store.Retention), retentionEvery, log) })

	sched := &probe.Scheduler{
		Scenario:    sc,
		Jobs:        jobs,
		Timeout:     cfg.Timeout,
		Jitter:      cfg.Jitter,
		Concurrency: cfg.Concurrency,
		Budget:      budget,
		Report:      func(o probe.Outcome) { record(ctx, db, log, o) },
	}
	log.Info("piplon running", "scenario", sc.ID(), "targets", len(jobs),
		"budget_usd", cfg.Budget.DailyUSD, "spent_today_usd", spent, "store", cfg.Store.Path)
	if err := sched.Run(ctx); err != nil {
		return err
	}
	log.Info("piplon stopped")
	return nil
}

func buildJobs(targets []config.Target) ([]probe.Job, error) {
	jobs := make([]probe.Job, len(targets))
	for i, t := range targets {
		p, err := probe.ForTarget(t)
		if err != nil {
			return nil, err
		}
		jobs[i] = probe.Job{Target: t.Name, Interval: t.Interval, Price: *t.Price, Prober: p}
	}
	return jobs, nil
}

// spentToday prices today's stored samples (UTC day, as the Budget counts).
// Samples of targets no longer configured use the default price.
func spentToday(ctx context.Context, db store.Store, targets []config.Target) (float64, error) {
	samples, err := db.Samples(ctx, store.Query{Since: time.Now().UTC().Truncate(24 * time.Hour)})
	if err != nil {
		return 0, fmt.Errorf("budget: %w", err)
	}
	prices := make(map[string]config.Price, len(targets))
	for _, t := range targets {
		prices[t.Name] = *t.Price
	}
	var usd float64
	for i, s := range samples {
		p, ok := prices[s.Target]
		if !ok {
			p = config.DefaultPrice
		}
		usd += probe.SamplesCost(p, samples[i:i+1])
	}
	return usd, nil
}

// record stores and logs one outcome. A probe cut short by shutdown is not a
// measurement of the target, so it is dropped.
func record(ctx context.Context, db store.Store, log *slog.Logger, o probe.Outcome) {
	attrs := []any{"target", o.Target, "scenario", o.Scenario}
	switch {
	case ctx.Err() != nil:
		log.Info("probe abandoned at shutdown", attrs...)
		return
	case o.Skipped != "":
		log.Warn("probe skipped", append(attrs, "reason", o.Skipped)...)
		return
	}
	if err := db.Insert(ctx, o.Samples()...); err != nil {
		log.Error("store insert failed", append(attrs, "err", err)...)
	}
	last := o.Turns[len(o.Turns)-1]
	expectOK := true
	for _, t := range o.Turns {
		expectOK = expectOK && t.ExpectOK
	}
	attrs = append(attrs, "probe_id", o.ProbeID, "turns", len(o.Turns), "expect_ok", expectOK,
		"ttft_ms", o.Turns[0].TTFT.Milliseconds(), "cost_usd", o.CostUSD)
	if last.Error != "" {
		log.Warn("probe failed", append(attrs, "turn", len(o.Turns), "http_status", last.HTTPStatus, "err", last.Error)...)
		return
	}
	log.Info("probe done", attrs...)
}

// newLogger writes JSON, or text when w is a terminal.
func newLogger(w io.Writer) *slog.Logger {
	if f, ok := w.(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			return slog.New(slog.NewTextHandler(w, nil))
		}
	}
	return slog.New(slog.NewJSONHandler(w, nil))
}
