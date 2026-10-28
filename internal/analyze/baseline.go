package analyze

import (
	"math"
	"slices"
	"time"

	"github.com/piplon-sh/piplon/internal/store"
)

// Window is how far back a baseline looks. MinHistory is how old a target's
// oldest probe in that window must be before anything is learned.
const (
	Window     = 28 * 24 * time.Hour
	MinHistory = 24 * time.Hour
)

// minProbes is the fewest healthy probes a bucket needs before its
// percentiles are trusted; a thinner bucket falls back to a wider one.
const minProbes = 20

// Stats describes one bucket of a baseline.
type Stats struct {
	Probes     int           // healthy probes behind TTFT50 and TTFT95
	TTFT50     time.Duration // p50 of the probes' TTFT
	TTFT95     time.Duration // p95 of the probes' TTFT
	Cached     int           // probes whose cache turns completed, healthy or not
	CacheShare float64       // share of Cached that read the cache on all of them
}

// Baseline is what a target's probes usually look like, per weekday and hour
// of the day in UTC, with the same hour on any weekday and the whole window
// as fallbacks.
type Baseline struct {
	ready bool
	week  [7][24]Stats
	hour  [24]Stats
	all   Stats
}

// At returns the stats for a probe starting at t: its weekday and hour if
// that bucket has enough probes, else the same hour on any weekday, else the
// whole window. ok is false while the target has less than MinHistory or no
// bucket is thick enough.
func (b Baseline) At(t time.Time) (Stats, bool) {
	if !b.ready {
		return Stats{}, false
	}
	t = t.UTC()
	for _, s := range []Stats{b.week[t.Weekday()][t.Hour()], b.hour[t.Hour()], b.all} {
		if s.Probes >= minProbes {
			return s, true
		}
	}
	return Stats{}, false
}

// summary is one probe reduced to what a baseline needs.
type summary struct {
	start            time.Time
	healthy          bool
	ttft             time.Duration
	cached, cacheHit bool
}

// bucket gathers the probes of one bucket before its stats are computed.
type bucket struct {
	ttfts        []time.Duration
	cached, hits int
}

func (b *bucket) add(p summary) {
	if p.healthy {
		b.ttfts = append(b.ttfts, p.ttft)
	}
	if p.cached {
		b.cached++
		if p.cacheHit {
			b.hits++
		}
	}
}

func (b *bucket) stats() Stats {
	s := Stats{Probes: len(b.ttfts), Cached: b.cached}
	if s.Probes > 0 {
		slices.Sort(b.ttfts)
		s.TTFT50, s.TTFT95 = percentile(b.ttfts, 0.50), percentile(b.ttfts, 0.95)
	}
	if b.cached > 0 {
		s.CacheShare = float64(b.hits) / float64(b.cached)
	}
	return s
}

// Learn builds a target's baseline from its samples over Window. Samples of
// another scenario are ignored, so a new scenario version starts from
// nothing. cacheFrom is the scenario's first turn expected to read the cache,
// 0 for none.
func Learn(samples []store.Sample, scenario string, cacheFrom int, now time.Time) Baseline {
	var week [7][24]bucket
	var hour [24]bucket
	var all bucket
	var b Baseline
	oldest := now
	for _, p := range summarize(samples, scenario, cacheFrom) {
		t := p.start.UTC()
		week[t.Weekday()][t.Hour()].add(p)
		hour[t.Hour()].add(p)
		all.add(p)
		if t.Before(oldest) {
			oldest = t
		}
	}
	for d := range week {
		for h := range week[d] {
			b.week[d][h] = week[d][h].stats()
		}
	}
	for h := range hour {
		b.hour[h] = hour[h].stats()
	}
	b.all = all.stats()
	b.ready = now.Sub(oldest) >= MinHistory
	return b
}

// summarize groups samples into probes. A probe is healthy when every turn
// completed and met its expectation; its TTFT is the median of its turns, so
// one odd turn does not move it. Its cache is known when at least one turn
// from cacheFrom completed, and a hit when all of those read the cache.
func summarize(samples []store.Sample, scenario string, cacheFrom int) []summary {
	var probes []summary
	index := map[string]int{}
	turns := map[string][]time.Duration{}
	for _, s := range samples {
		if s.Scenario != scenario {
			continue
		}
		i, seen := index[s.ProbeID]
		if !seen {
			i = len(probes)
			index[s.ProbeID] = i
			probes = append(probes, summary{start: s.Time, healthy: true, cacheHit: true})
		}
		p := &probes[i]
		if s.Time.Before(p.start) {
			p.start = s.Time
		}
		p.healthy = p.healthy && s.Error == "" && s.ExpectOK && s.TTFT > 0
		turns[s.ProbeID] = append(turns[s.ProbeID], s.TTFT)
		if cacheFrom > 0 && s.Turn >= cacheFrom && s.Error == "" {
			p.cached = true
			p.cacheHit = p.cacheHit && s.CacheReadTokens > 0
		}
	}
	for id, i := range index {
		t := turns[id]
		slices.Sort(t)
		probes[i].ttft = t[(len(t)-1)/2]
		probes[i].cacheHit = probes[i].cached && probes[i].cacheHit
	}
	return probes
}

// percentile is the nearest-rank percentile of sorted, q in (0, 1].
func percentile(sorted []time.Duration, q float64) time.Duration {
	return sorted[int(math.Ceil(q*float64(len(sorted))))-1]
}
