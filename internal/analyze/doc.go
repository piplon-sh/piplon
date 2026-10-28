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
// Status applies hysteresis. The first probe's verdict sets State at once, so
// a bad key shows as down after one probe; after that, State changes only
// after Confirm (3) probes in a row agree on the new one, to open as well as
// to close, and until then the candidate is Pending with its Count. The first
// State is not reported as a change. Hard reports the failure states.
//
// Analyzer keeps a Status per target. Observe learns the target's baseline
// from the store on its first probe and every hour after, from the samples
// before that probe, then classifies and applies hysteresis. Skipped ticks
// (budget, overlap) leave the status unchanged. A store error keeps the
// previous baseline and is returned for the caller to log.
package analyze
