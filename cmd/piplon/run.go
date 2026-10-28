package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/piplon-sh/piplon/internal/analyze"
	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/probe"
	"github.com/piplon-sh/piplon/internal/scenario"
	"github.com/piplon-sh/piplon/internal/store"
	"github.com/piplon-sh/piplon/internal/version"
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
			if f, ok := cmd.OutOrStdout().(*os.File); ok && term.IsTerminal(f.Fd()) {
				view := &liveTable{w: f, width: func() int { return termWidth(f) }}
				return run(cmd.Context(), path, view.logger(), view)
			}
			return run(cmd.Context(), path, newLogger(cmd.ErrOrStderr()), nil)
		},
	}
	cmd.Flags().StringVarP(&path, "config", "c", "piplon.yaml", "path to the config file")
	return cmd
}

// run probes until ctx is cancelled, storing every outcome. A non-nil view
// shows the live table.
func run(ctx context.Context, path string, log *slog.Logger, view *liveTable) error {
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

	an := analyze.New(db, sc.ID(), sc.Cache.ExpectHitFromTurn)
	names := make([]string, len(jobs))
	for i, j := range jobs {
		names[i] = j.Target
	}
	closed, err := an.Restore(ctx, names, time.Now())
	for _, inc := range closed {
		logIncident(log, "incident closed", inc)
	}
	if err != nil {
		return err
	}
	sched := &probe.Scheduler{
		Scenario:    sc,
		Jobs:        jobs,
		Timeout:     cfg.Timeout,
		Jitter:      cfg.Jitter,
		Concurrency: cfg.Concurrency,
		Budget:      budget,
		Report: func(o probe.Outcome) {
			record(ctx, db, log, o)
			if ctx.Err() != nil {
				return
			}
			st := observe(ctx, an, log, o)
			if view != nil {
				view.update(ctx, o, st)
			}
		},
		Confirm: func(o probe.Outcome) bool { return an.Confirming(o.Target) },
	}
	if view != nil {
		header := func() string {
			return fmt.Sprintf("piplon %s · %s · %d targets · every %s · %.4f of %.2f USD today",
				version.Version, sc.ID(), len(jobs), short(cfg.Interval), budget.Spent(), cfg.Budget.DailyUSD)
		}
		view.start(header, names, db)
		wg.Go(func() { view.run(ctx) })
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

// observe judges one outcome and logs a confirmed change of state.
func observe(ctx context.Context, an *analyze.Analyzer, log *slog.Logger, o probe.Outcome) analyze.Status {
	st, err := an.Observe(ctx, o)
	if err != nil {
		log.Error("baseline not learned", "target", o.Target, "scenario", o.Scenario, "err", err)
	}
	if st.Changed {
		attrs := []any{"target", o.Target, "scenario", o.Scenario, "state", st.State}
		if st.State == analyze.OK {
			log.Info("target state changed", attrs...)
		} else {
			log.Warn("target state changed", append(attrs, "detail", st.Last.Detail)...)
		}
	}
	switch st.Event {
	case analyze.Opened:
		logIncident(log, "incident opened", st.Incident)
	case analyze.Updated:
		logIncident(log, "incident updated", st.Incident)
	case analyze.Closed:
		logIncident(log, "incident closed", st.Incident)
	}
	return st
}

// logIncident logs an incident event. A close says whether the target came
// back to ok (resolved) or the incident was closed administratively.
func logIncident(log *slog.Logger, msg string, inc store.Incident) {
	attrs := []any{"target", inc.Target, "scenario", inc.Scenario, "incident", inc.ID, "state", inc.State,
		"cause", inc.Cause, "detail", inc.Detail}
	if inc.Closed.IsZero() {
		log.Warn(msg, attrs...)
		return
	}
	log.Info(msg, append(attrs, "resolved", inc.Resolved, "close_cause", inc.CloseCause)...)
}

// short drops zero minutes and seconds: 5m, 1h, 1m30s.
func short(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// termWidth is the terminal's width, 80 when it cannot be read.
func termWidth(f *os.File) int {
	if w, _, err := term.GetSize(f.Fd()); err == nil && w > 0 {
		return w
	}
	return 80
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
