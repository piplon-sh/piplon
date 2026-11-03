// Package serve exposes what the store holds over HTTP. It only reads the
// store, so piplon serve can run beside piplon run on the same database, and
// piplon run can serve the same endpoints itself.
//
// Handler returns the mux for the dashboard and three endpoints:
//
//   - / is the dashboard, rendered on the server with html/template: a top
//     bar (UTC clock, age of the latest probe, the day's spend against
//     budget.daily_usd), a banner naming each target not ok, most severe
//     first, with its cause and since when, the targets table (state, TTFT
//     p50, error share and cache over the last hour, as analyze.Summarize
//     computes them for the terminal, and the latest probe's age), and the
//     incidents of the last 7 days. An open incident shows its state, a
//     closed one resolved, or closed for an administrative close.
//   - /live is the part of the page that changes; htmx swaps it in every
//     10 s. A store it cannot read is a 503, which htmx does not swap in.
//   - /assets/ serves the stylesheet, htmx 2.0.11, the mark and IBM Plex
//     Sans and Mono (Latin-1, SIL Open Font License, fonts/OFL.txt), all
//     embedded in the binary: nothing is loaded from elsewhere, and every
//     response sets a Content-Security-Policy of 'self'.
//   - /healthz answers 200 while the process runs.
//   - /readyz answers 200 when the store is readable, 503 otherwise. It does
//     not depend on how recent the probes are, so a stopped piplon run never
//     takes the metrics away; piplon_last_probe_timestamp_seconds tells that.
//   - /metrics is the Prometheus endpoint.
//
// Counters and histograms count the samples stored since the previous scrape,
// starting from the newest sample when Handler is called: a restart resets
// them to 0, which Prometheus reads as a reset, and never replays history.
// Labels are target and scenario, plus:
//
//   - piplon_probes_total{result}: ok when every turn completed and met its
//     expectation, else failed;
//   - piplon_responses_total{turn, code}: the HTTP status, none when no HTTP
//     answer arrived (DNS, TLS, timeout);
//   - piplon_stream_cuts_total{turn} and piplon_refusals_total{turn};
//   - piplon_cache_turns_total{result}: hit or miss, for the completed turns
//     from the scenario's first cache turn;
//   - piplon_tokens_total{kind}: input (uncached), output, cache_read and
//     cache_write;
//   - piplon_ttft_seconds{turn} and piplon_turn_duration_seconds{turn},
//     histograms; a turn with no first token has no TTFT.
//
// Gauges are reread from the store on every scrape:
//
//   - piplon_last_probe_timestamp_seconds and piplon_last_probe_success, for
//     each configured target's latest probe;
//   - piplon_target_state{state}, for each configured target piplon run has
//     judged: 1 for its current state after hysteresis, 0 for every other
//     state, so a change never leaves a gap; and
//     piplon_target_state_since_seconds, when that state began;
//   - piplon_incident_open{state}, 1 per open incident;
//   - piplon_notifications{channel, status} (pending, delivered, abandoned)
//     and piplon_notification_oldest_pending_seconds{channel};
//   - piplon_budget_daily_usd and piplon_budget_spent_usd, the cost of the
//     probes stored since 00:00 UTC at the configured prices;
//   - piplon_build_info{version}, always 1.
//
// The Go runtime and process collectors are included. No label carries an
// error message or an incident ID, so the number of series stays bounded.
//
// /api/v1/targets and /api/v1/incidents answer JSON from the same snapshot
// the dashboard renders, so the two never disagree. The v1 contract only
// grows: fields are added, never renamed, removed or retyped, and every field
// is present, null when it has no value. Times are RFC 3339 in UTC to the
// second, durations integer seconds (TTFT in seconds with milliseconds).
//
//   - /api/v1/targets: api_version, generated_at, budget (daily_usd,
//     spent_today_usd) and, per configured target in config order, name,
//     format, model, scenario, state, since, cause, incident_id, last_probe
//     (at, state, detail) and last_hour (probes, error_ratio from 0 to 1,
//     ttft_p50_seconds, cache: hit, miss or n/a). A target never probed has
//     null in place of its state.
//   - /api/v1/incidents: api_version, generated_at, since, and the incidents
//     open at since or after, newest first: id, target, scenario, status
//     (open, resolved, or closed for an administrative close), state, cause,
//     detail, opened_at, updated_at, closed_at, close_cause and
//     duration_seconds, up to generated_at while open. ?since= takes an RFC
//     3339 time or a duration back from now (90m, 24h, 7d), 7 days by
//     default; ?state=open keeps the open ones.
//
// Bad parameters are a 400 and an unreadable store a 503, both with
// {"error": "..."}. The handler is given the targets' names, formats and
// models to show, never their keys or URLs, and no webhook URL.
//
// Serve serves a handler on a listener until its context is cancelled.
package serve
