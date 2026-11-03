package analyze

import (
	"slices"
	"time"

	"github.com/piplon-sh/piplon/internal/probe"
	"github.com/piplon-sh/piplon/internal/store"
)

// Recent sums up a target's samples over a short window, the last hour for
// the live table and the dashboard.
type Recent struct {
	Probes int
	ErrPct float64       // share of failed probes, in %; a refusal is a state of its own, not an error
	TTFT   time.Duration // median TTFT of the completed turns, 0 when none
	Cached bool          // some turn read the cache
	// Cache is the latest probe's: hit, miss or -, the rule Classify uses,
	// and n/a for a miss on a target that read no cache in the window: it
	// never cached, it did not stop.
	Cache string
}

// Summarize sums up samples in time order. cacheFrom is the scenario's first
// turn expected to read the cache, 0 for none.
func Summarize(samples []store.Sample, cacheFrom int) Recent {
	failed := map[string]bool{}
	var ttfts []time.Duration
	var r Recent
	for _, s := range samples {
		r.Cached = r.Cached || s.CacheReadTokens > 0
		bad := (s.Error != "" && !(probe.Turn{Error: s.Error}).Refused()) || (s.Error == "" && !s.ExpectOK)
		failed[s.ProbeID] = failed[s.ProbeID] || bad
		if s.Error == "" && s.TTFT > 0 {
			ttfts = append(ttfts, s.TTFT)
		}
	}
	r.Probes = len(failed)
	for _, f := range failed {
		if f {
			r.ErrPct++
		}
	}
	if r.Probes > 0 {
		r.ErrPct = 100 * r.ErrPct / float64(r.Probes)
	}
	if len(ttfts) > 0 {
		slices.Sort(ttfts)
		r.TTFT = ttfts[len(ttfts)/2]
	}
	r.Cache = latestCache(samples, cacheFrom)
	if r.Cache == "miss" && !r.Cached {
		r.Cache = "n/a"
	}
	return r
}

// latestCache is cacheState for the turns of the latest probe in samples.
func latestCache(samples []store.Sample, cacheFrom int) string {
	if len(samples) == 0 {
		return "-"
	}
	last := samples[len(samples)-1].ProbeID
	var turns []probe.Turn
	for _, s := range samples {
		if s.ProbeID == last {
			turns = append(turns, probe.Turn{Error: s.Error, CacheReadTokens: s.CacheReadTokens})
		}
	}
	return cacheState(turns, cacheFrom)
}
