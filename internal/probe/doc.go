// Package probe sends scenarios to targets on a schedule and measures what
// comes back.
//
// A Prober runs one scenario against one target and returns a Result with one
// Turn per conversation turn it attempted. Probers never return an error:
// every failure is recorded in the failing Turn (HTTPStatus, ProviderError,
// Error), so a failure is a measurement like any other. Provider adapters
// implement Prober. Turn.InputTokens counts uncached input only; adapters
// normalize providers that include cached tokens in their input count.
//
// Anthropic is the adapter for the Anthropic Messages API. It streams every
// turn, puts prompt-cache markers on the system prompt and the first user
// turn, and replays the real answers as history. Per turn it records TTFT
// from the request being fully written to the first content delta, usage and
// cache tokens, HTTP and provider errors, and a cut stream (closed before
// message_stop). A transport, HTTP or stream failure ends the conversation; an
// answer that misses its expectation does not, so cache reads on later turns
// are still measured. Errors never contain the key.
//
// Scheduler runs one Job per target until its context is cancelled:
//
//   - a probe at start, then one per Job.Interval;
//   - each probe waits a random delay in [0, Jitter), then a slot among
//     Concurrency, then runs under Timeout;
//   - a tick that arrives while the previous probe of the same target is
//     still pending is skipped and reported as SkipOverlap;
//   - before sending, the probe reserves its estimated cost in the Budget;
//     if it does not fit, the probe is reported as SkipBudget instead of
//     running. After the probe the reservation is replaced by the actual cost.
//
// Every tick produces exactly one Outcome through Scheduler.Report, except a
// tick abandoned at shutdown before it was sent. Report is
// called from many goroutines. Outcome.Samples converts an Outcome to store rows.
//
// Budget is a daily USD cap that resets at 00:00 UTC. The estimate prices
// every turn as uncached input at the scenario's target size plus maximum
// output, so it errs high. Seed restores the day's spend after a restart.
package probe
