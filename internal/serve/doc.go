// Package serve exposes what the store holds over HTTP. It only reads the
// store, so piplon serve can run beside piplon run on the same database, and
// piplon run can serve the same endpoints itself.
//
// Handler returns the mux for three endpoints:
//
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
// Serve serves a handler on a listener until its context is cancelled.
package serve
