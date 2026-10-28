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
package analyze
