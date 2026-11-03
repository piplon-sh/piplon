package serve

import (
	"context"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/piplon-sh/piplon/internal/analyze"
	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/probe"
	"github.com/piplon-sh/piplon/internal/store"
)

// Buckets in seconds. Turn 1 is never cached and its TTFT is usually a few
// hundred ms to a few seconds; the top buckets catch slow paths and timeouts.
var (
	ttftBuckets     = []float64{0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30}
	durationBuckets = []float64{0.25, 0.5, 1, 2, 4, 8, 15, 30, 60}
)

// lastProbe is what the gauges say about a target's latest probe.
type lastProbe struct {
	scenario string
	at       time.Time
	ok       bool
}

// metrics follows the store. Counters and histograms add the samples stored
// since the previous scrape, starting from the newest sample at start, so a
// restart is a plain counter reset and never replays history. Gauges are
// reread from the store on every scrape. The handler serializes scrapes, so
// nothing here is locked.
type metrics struct {
	db     store.Store
	opts   Options
	prices map[string]config.Price
	reg    *prometheus.Registry

	cursor    int64                // ID of the newest sample counted
	last      map[string]lastProbe // by configured target
	day       time.Time            // the UTC day spent covers
	spent     float64
	incidents []store.Incident
	outbox    []store.ChannelOutbox
	states    []store.TargetState
	now       time.Time // of the latest refresh

	probes, responses, cuts, refusals, cache, tokens *prometheus.CounterVec
	ttft, duration                                   *prometheus.HistogramVec
}

func counter(name, help string, labels ...string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help},
		append([]string{"target", "scenario"}, labels...))
}

func histogram(name, help string, buckets []float64) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: help, Buckets: buckets},
		[]string{"target", "scenario", "turn"})
}

func newMetrics(ctx context.Context, db store.Store, opts Options) (*metrics, error) {
	m := &metrics{
		db: db, opts: opts, reg: prometheus.NewRegistry(),
		prices: make(map[string]config.Price, len(opts.Targets)),
		last:   make(map[string]lastProbe, len(opts.Targets)),

		probes:    counter("piplon_probes_total", "Probes completed, by result: ok when every turn completed and met its expectation.", "result"),
		responses: counter("piplon_responses_total", "Turns by HTTP status of the answer, none when no HTTP answer arrived.", "turn", "code"),
		cuts:      counter("piplon_stream_cuts_total", "Turns whose stream closed before its end.", "turn"),
		refusals:  counter("piplon_refusals_total", "Turns the model declined to answer.", "turn"),
		cache:     counter("piplon_cache_turns_total", "Completed turns expected to read the prompt cache, by result.", "result"),
		tokens:    counter("piplon_tokens_total", "Tokens reported by the provider, by kind; input counts uncached input only.", "kind"),
		ttft:      histogram("piplon_ttft_seconds", "Time from the request being written to the first content delta.", ttftBuckets),
		duration:  histogram("piplon_turn_duration_seconds", "Time from the request being written to the end of the turn.", durationBuckets),
	}
	for _, t := range opts.Targets {
		m.prices[t.Name] = *t.Price
		m.last[t.Name] = lastProbe{}
	}
	buildInfo := prometheus.NewGauge(prometheus.GaugeOpts{Name: "piplon_build_info",
		Help: "Always 1, labelled with the piplon version.", ConstLabels: prometheus.Labels{"version": opts.Version}})
	buildInfo.Set(1)
	m.reg.MustRegister(m.probes, m.responses, m.cuts, m.refusals, m.cache, m.tokens, m.ttft, m.duration,
		buildInfo, gauges{m})
	return m, m.start(ctx)
}

// start places the cursor on the newest sample, takes each target's latest
// probe for the gauges, and prices the samples of the day up to the cursor.
func (m *metrics) start(ctx context.Context) error {
	latest, err := m.db.LatestProbes(ctx)
	if err != nil {
		return err
	}
	for _, p := range byProbe(latest) {
		m.cursor = max(m.cursor, p[len(p)-1].ID)
		m.see(p)
	}
	m.now = m.opts.Now()
	m.day = m.now.UTC().Truncate(24 * time.Hour)
	today, err := m.db.Samples(ctx, store.Query{Since: m.day})
	if err != nil {
		return err
	}
	for _, s := range today {
		if s.ID <= m.cursor {
			m.spent += m.cost(s)
		}
	}
	return m.refreshGauges(ctx)
}

// refresh counts the samples stored since the last one, then rereads the
// gauges.
func (m *metrics) refresh(ctx context.Context) error {
	samples, err := m.db.SamplesAfter(ctx, m.cursor)
	if err != nil {
		return err
	}
	m.now = m.opts.Now()
	if day := m.now.UTC().Truncate(24 * time.Hour); day.After(m.day) {
		m.day, m.spent = day, 0
	}
	for _, p := range byProbe(samples) {
		m.count(p)
		m.see(p)
		m.cursor = p[len(p)-1].ID
	}
	return m.refreshGauges(ctx)
}

func (m *metrics) refreshGauges(ctx context.Context) error {
	var err error
	if m.incidents, err = m.db.OpenIncidents(ctx); err != nil {
		return err
	}
	if m.outbox, err = m.db.Outbox(ctx); err != nil {
		return err
	}
	m.states, err = m.db.TargetStates(ctx)
	return err
}

// byProbe splits samples in ID order into probes. A probe's turns are
// inserted together, so they are next to each other.
func byProbe(samples []store.Sample) [][]store.Sample {
	var out [][]store.Sample
	for i, s := range samples {
		if i == 0 || s.ProbeID != samples[i-1].ProbeID {
			out = append(out, nil)
		}
		out[len(out)-1] = append(out[len(out)-1], s)
	}
	return out
}

// count adds one probe to the counters and histograms, and to the day's
// spend.
func (m *metrics) count(p []store.Sample) {
	target, scenario := p[0].Target, p[0].Scenario
	result := "failed"
	if succeeded(p) {
		result = "ok"
	}
	m.probes.WithLabelValues(target, scenario, result).Inc()
	for _, s := range p {
		turn := strconv.Itoa(s.Turn)
		code := "none"
		if s.HTTPStatus != 0 {
			code = strconv.Itoa(s.HTTPStatus)
		}
		m.responses.WithLabelValues(target, scenario, turn, code).Inc()
		if s.StreamCut {
			m.cuts.WithLabelValues(target, scenario, turn).Inc()
		}
		if (probe.Turn{Error: s.Error}).Refused() {
			m.refusals.WithLabelValues(target, scenario, turn).Inc()
		}
		if from := m.opts.CacheFrom; from >= 1 && s.Turn >= from && s.Error == "" {
			hit := "miss"
			if s.CacheReadTokens > 0 {
				hit = "hit"
			}
			m.cache.WithLabelValues(target, scenario, hit).Inc()
		}
		for kind, n := range map[string]int{"input": s.InputTokens, "output": s.OutputTokens,
			"cache_read": s.CacheReadTokens, "cache_write": s.CacheWriteTokens} {
			m.tokens.WithLabelValues(target, scenario, kind).Add(float64(n))
		}
		if s.TTFT > 0 {
			m.ttft.WithLabelValues(target, scenario, turn).Observe(s.TTFT.Seconds())
		}
		m.duration.WithLabelValues(target, scenario, turn).Observe(s.Total.Seconds())
		if !s.Time.Before(m.day) {
			m.spent += m.cost(s)
		}
	}
}

// see keeps a probe as its target's latest when the target is configured.
func (m *metrics) see(p []store.Sample) {
	if _, ok := m.last[p[0].Target]; ok {
		m.last[p[0].Target] = lastProbe{scenario: p[0].Scenario, at: p[0].Time, ok: succeeded(p)}
	}
}

// succeeded is true when every turn completed and met its expectation.
func succeeded(p []store.Sample) bool {
	for _, s := range p {
		if s.Error != "" || !s.ExpectOK {
			return false
		}
	}
	return true
}

// cost prices a sample at its target's price, the default one for a target
// no longer configured, as piplon run does.
func (m *metrics) cost(s store.Sample) float64 {
	p, ok := m.prices[s.Target]
	if !ok {
		p = config.DefaultPrice
	}
	return probe.SamplesCost(p, []store.Sample{s})
}

// gauges reports what the latest refresh read.
type gauges struct{ m *metrics }

var (
	lastProbeAt = prometheus.NewDesc("piplon_last_probe_timestamp_seconds",
		"When the target's latest probe started, in Unix seconds.", []string{"target", "scenario"}, nil)
	lastProbeOK = prometheus.NewDesc("piplon_last_probe_success",
		"1 when every turn of the target's latest probe completed and met its expectation, else 0.", []string{"target", "scenario"}, nil)
	incidentOpen = prometheus.NewDesc("piplon_incident_open",
		"1 for each open incident, labelled with its state.", []string{"target", "scenario", "state"}, nil)
	notifications = prometheus.NewDesc("piplon_notifications",
		"Notifications in the outbox, by channel and status.", []string{"channel", "status"}, nil)
	oldestPending = prometheus.NewDesc("piplon_notification_oldest_pending_seconds",
		"Age of the oldest notification not delivered yet, 0 when none is pending.", []string{"channel"}, nil)
	targetState = prometheus.NewDesc("piplon_target_state",
		"1 for the target's current state, 0 for the others, as piplon run judges it after hysteresis.", []string{"target", "scenario", "state"}, nil)
	targetSince = prometheus.NewDesc("piplon_target_state_since_seconds",
		"When the target's current state began, in Unix seconds.", []string{"target", "scenario"}, nil)
	budgetDaily = prometheus.NewDesc("piplon_budget_daily_usd",
		"The daily probing budget, budget.daily_usd.", nil, nil)
	budgetSpent = prometheus.NewDesc("piplon_budget_spent_usd",
		"What stored probes cost since 00:00 UTC.", nil, nil)
)

func (gauges) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{lastProbeAt, lastProbeOK, incidentOpen, notifications, oldestPending,
		targetState, targetSince, budgetDaily, budgetSpent} {
		ch <- d
	}
}

func (g gauges) Collect(ch chan<- prometheus.Metric) {
	m := g.m
	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	for target, p := range m.last {
		if p.at.IsZero() {
			continue // never probed
		}
		ok := 0.0
		if p.ok {
			ok = 1
		}
		gauge(lastProbeAt, float64(p.at.UnixMilli())/1e3, target, p.scenario)
		gauge(lastProbeOK, ok, target, p.scenario)
	}
	for _, ts := range m.states {
		if _, ok := m.last[ts.Target]; !ok {
			continue // not configured here
		}
		for _, state := range analyze.States {
			v := 0.0
			if string(state) == ts.State {
				v = 1
			}
			gauge(targetState, v, ts.Target, ts.Scenario, string(state))
		}
		gauge(targetSince, float64(ts.Since.UnixMilli())/1e3, ts.Target, ts.Scenario)
	}
	for _, inc := range m.incidents {
		gauge(incidentOpen, 1, inc.Target, inc.Scenario, inc.State)
	}
	for _, c := range m.outbox {
		gauge(notifications, float64(c.Pending), c.Channel, "pending")
		gauge(notifications, float64(c.Delivered), c.Channel, "delivered")
		gauge(notifications, float64(c.Abandoned), c.Channel, "abandoned")
		age := 0.0
		if !c.OldestPending.IsZero() {
			age = max(m.now.Sub(c.OldestPending).Seconds(), 0)
		}
		gauge(oldestPending, age, c.Channel)
	}
	gauge(budgetDaily, m.opts.DailyUSD)
	gauge(budgetSpent, m.spent)
}
