package analyze

import (
	"fmt"
	"slices"
	"time"

	"github.com/piplon-sh/piplon/internal/probe"
)

// State is what a target is going through.
type State string

// States, from least to most severe after OK.
const (
	OK        State = "ok"
	CacheMiss State = "cache_miss" // stopped reading a cache it usually reads
	Slow      State = "slow"       // TTFT above what is usual at this time
	Refused   State = "refused"    // the model declined to answer
	Errors    State = "errors"     // a turn failed after the first answered
	Down      State = "down"       // the first turn got no answer
	Gone      State = "gone"       // the model is not found
)

var severity = map[State]int{OK: 0, CacheMiss: 1, Slow: 2, Refused: 3, Errors: 4, Down: 5, Gone: 6}

// Hard reports whether s needs no baseline to be judged.
func (s State) Hard() bool { return severity[s] >= severity[Refused] }

// A probe is slow when its TTFT exceeds both the usual p95 and slowFactor
// times the usual p50; the second bound keeps a very steady target, whose p95
// is close to its p50, from turning slow on noise. A cache miss counts when
// the target usually read the cache in at least cachedShare of its probes.
const (
	slowFactor  = 1.5
	cachedShare = 0.8
)

// Verdict is one probe judged on its own, before hysteresis.
type Verdict struct {
	State  State
	Detail string // why it is not ok, empty when it is
	Cache  string // hit, miss, or - when no cache turn completed or none is expected
}

// Classify judges one probe that was sent. Failures come first and need no
// baseline; slow and cache_miss need base, so learned false never yields
// them. cacheFrom is the scenario's first turn expected to read the cache, 0
// for none.
func Classify(o probe.Outcome, base Stats, learned bool, cacheFrom int) Verdict {
	v := Verdict{State: OK, Cache: cacheState(o.Turns, cacheFrom)}
	for i, t := range o.Turns {
		if s, detail := failure(i, t); severity[s] > severity[v.State] {
			v.State, v.Detail = s, detail
		}
	}
	if v.State != OK || !learned {
		return v
	}
	if ttft := middle(o.Turns); float64(ttft) > max(float64(base.TTFT95), slowFactor*float64(base.TTFT50)) {
		v.State = Slow
		v.Detail = fmt.Sprintf("TTFT %d ms, usually %d ms", ttft.Milliseconds(), base.TTFT50.Milliseconds())
		return v
	}
	if v.Cache == "miss" && base.CacheShare >= cachedShare {
		v.State = CacheMiss
		v.Detail = fmt.Sprintf("cache not read, usually read by %.0f %% of probes", 100*base.CacheShare)
	}
	return v
}

// failure is the state of one turn, OK when it completed and met its
// expectation. i is the turn's index.
func failure(i int, t probe.Turn) (State, string) {
	switch {
	case t.HTTPStatus == 404 || t.ProviderError == "not_found_error" || t.ProviderError == "model_not_found":
		return Gone, fmt.Sprintf("turn %d: %s", i+1, t.Error)
	case t.Refused():
		return Refused, fmt.Sprintf("turn %d: %s", i+1, t.Error)
	case t.Error != "" && i == 0:
		return Down, fmt.Sprintf("turn 1: %s", t.Error)
	case t.Error != "":
		return Errors, fmt.Sprintf("turn %d: %s", i+1, t.Error)
	case !t.ExpectOK:
		return Errors, fmt.Sprintf("turn %d: answer missed its expectation", i+1)
	}
	return OK, ""
}

// cacheState is hit when every completed turn from cacheFrom read the cache,
// the same rule Learn uses.
func cacheState(turns []probe.Turn, cacheFrom int) string {
	if cacheFrom < 1 || len(turns) < cacheFrom {
		return "-"
	}
	state := "-"
	for _, t := range turns[cacheFrom-1:] {
		switch {
		case t.Error != "":
		case t.CacheReadTokens == 0:
			return "miss"
		default:
			state = "hit"
		}
	}
	return state
}

// middle is the median TTFT of the turns, the probe TTFT Learn uses.
func middle(turns []probe.Turn) time.Duration {
	ttfts := make([]time.Duration, len(turns))
	for i, t := range turns {
		ttfts[i] = t.TTFT
	}
	slices.Sort(ttfts)
	return ttfts[(len(ttfts)-1)/2]
}

// Confirm is how many probes in a row must agree before a state changes, to
// open as well as to close.
const Confirm = 3

// Status is a target's state after hysteresis.
type Status struct {
	State   State   // the first probe's verdict, then the last confirmed one
	Pending State   // a different state seen Count probes in a row, empty if none
	Count   int     // probes in a row in Pending
	Last    Verdict // the latest probe sent
	Changed bool    // the latest probe confirmed a new State; never on the first
	Failing int     // hard failures in a row, of any state, up to the latest probe
}

// Confirming reports whether the latest probe failed hard and fewer than
// Confirm probes in a row have, so the failure is worth checking again soon.
func (s Status) Confirming() bool { return s.Failing > 0 && s.Failing < Confirm }

// observe applies one verdict. The first one sets State directly, so a bad
// key shows as down after one probe; hysteresis applies to every change after.
func (s *Status) observe(v Verdict) {
	s.Last, s.Changed = v, false
	if v.State.Hard() {
		s.Failing++
	} else {
		s.Failing = 0
	}
	switch {
	case s.State == "":
		s.State = v.State
		return
	case v.State == s.State:
		s.Pending, s.Count = "", 0
		return
	case v.State != s.Pending:
		s.Pending, s.Count = v.State, 0
	}
	s.Count++
	if s.Count >= Confirm {
		s.State, s.Pending, s.Count, s.Changed = v.State, "", 0, true
	}
}
