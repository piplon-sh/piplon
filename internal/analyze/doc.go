// Package analyze learns what a target's probes usually look like and, from
// there, which state the target is in.
//
// Learn builds a Baseline from a target's samples over the last Window (28
// days). Samples are grouped into probes by ProbeID; only probes of the given
// scenario count, so a new scenario version is never compared with an old
// one. A probe is healthy when every turn completed and met its expectation;
// its TTFT is the median of its turns. Its cache is known when a turn from
// the scenario's first cache turn completed, and a hit when all of those read
// the cache.
//
// A Baseline holds Stats per weekday and hour of the day in UTC, per hour on
// any weekday, and for the whole window: healthy probe count, TTFT p50 and
// p95, and the share of probes that read the cache. Baseline.At picks the
// narrowest of the three with at least 20 healthy probes, and reports nothing
// until the target's oldest probe in the window is MinHistory (24 h) old.
//
// Classify judges one probe into a Verdict: a State, a detail naming the
// failing turn or the figure behind a learned state, and the cache result
// (hit, miss or -). Failures need no baseline and rank gone (404, model not
// found), down (turn 1 got no answer), errors (a later turn failed, or any
// answer missed its expectation), then refused (the model declined: its own
// state, neither errors nor down). A probe with no failure is slow when its
// TTFT exceeds both the usual p95 and 1.5 times the usual p50, else
// cache_miss when it missed the cache on a target that usually read it in at
// least 80 % of probes. A target that never reads the cache is never a miss.
// Without a baseline neither learned state is given.
//
// Every non-ok Verdict carries a probable cause, one sentence an operator can
// act on: a rejected key (401, 403), a rate limit (429), an overloaded
// provider (529), a server error, a timeout, a cut stream, no HTTP answer
// (DNS, TLS, network), a model not found, a refusal with its category, or a
// slower than usual or no longer cached path.
//
// Status applies hysteresis. The first probe's verdict sets State at once, so
// a bad key shows as down after one probe; after that, State changes only
// after Confirm (3) probes in a row agree on the new one, to open as well as
// to close, and until then the candidate is Pending with its Count. The first
// State is not reported as a change. Hard reports the failure states.
// Failing counts hard failures in a row, of any state; Confirming is true
// while it is between 1 and Confirm, when the scheduler probes again after
// 30 s rather than waiting for the interval.
//
// Analyzer keeps a Status per target. Observe learns the target's baseline
// from the store on its first probe and every hour after, from the samples
// before that probe, then classifies and applies hysteresis. Skipped ticks
// (budget, overlap) leave the status unchanged. A store error keeps the
// previous baseline and is returned for the caller to log. Confirming tells
// the scheduler whether a target's latest probe needs confirming.
//
// Observe also keeps one incident per episode in the store. It opens once the
// target has been in a non-ok State for Confirm probes in a row, including
// right after start, where the first probe already set the State. While open,
// a confirmed change of state updates its state and cause; back to ok, it is
// closed as resolved. Status.Event and Status.Incident report what the latest
// probe did. A store error leaves the incident as it was and the next probe
// tries again. Restore, at start, resumes the open incidents of configured
// targets, which start in the incident's state, and closes the others as
// "target removed from config", never resolved.
package analyze
