package analyze

import (
	"fmt"
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/store"
)

const ms = time.Millisecond

// monday is a Monday at midnight UTC.
var monday = time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)

// healthy is a standard-v1 probe whose three turns answer in ttft and read
// the cache from turn 2.
func healthy(id string, start time.Time, ttft time.Duration) []store.Sample {
	return withTurns(id, start, ttft, ttft, ttft)
}

func withTurns(id string, start time.Time, ttfts ...time.Duration) []store.Sample {
	var out []store.Sample
	for i, t := range ttfts {
		s := store.Sample{ProbeID: id, Time: start.Add(time.Duration(i) * time.Second), Scenario: "standard-v1",
			Turn: i + 1, TTFT: t, ExpectOK: true}
		if i > 0 {
			s.CacheReadTokens = 4000
		}
		out = append(out, s)
	}
	return out
}

// every probes from start to end, with the TTFT ttft gives for each start.
func every(step time.Duration, start, end time.Time, ttft func(time.Time) time.Duration) []store.Sample {
	var out []store.Sample
	for t := start; t.Before(end); t = t.Add(step) {
		out = append(out, healthy(t.Format(time.RFC3339), t, ttft(t))...)
	}
	return out
}

func flat(time.Time) time.Duration { return 1000 * ms }

func TestLearnNeedsADayOfHistory(t *testing.T) {
	for _, tt := range []struct {
		history time.Duration
		ready   bool
	}{{23 * time.Hour, false}, {25 * time.Hour, true}} {
		now := monday.Add(tt.history)
		b := Learn(every(5*time.Minute, monday, now, flat), "standard-v1", 2, now)
		if _, ok := b.At(now); ok != tt.ready {
			t.Errorf("%v of history: ready=%v, want %v", tt.history, ok, tt.ready)
		}
	}
}

func TestBaselineFollowsWeekdayAndHour(t *testing.T) {
	// Mondays at 09h UTC are slow; two weeks every 5 min give 24 probes per bucket.
	now := monday.Add(14 * 24 * time.Hour)
	b := Learn(every(5*time.Minute, monday, now, func(t time.Time) time.Duration {
		if t.Weekday() == time.Monday && t.Hour() == 9 {
			return 2000 * ms
		}
		return 1000 * ms
	}), "standard-v1", 2, now)
	for _, tt := range []struct {
		at   time.Time
		want time.Duration
	}{{monday.Add(9*time.Hour + 30*time.Minute), 2000 * ms}, {monday.Add(33*time.Hour + 30*time.Minute), 1000 * ms}} {
		s, ok := b.At(tt.at)
		if !ok || s.TTFT50 != tt.want || s.Probes != 24 {
			t.Errorf("%s: got %+v ok=%v, want p50 %v from 24 probes", tt.at.Format("Mon 15:04"), s, ok, tt.want)
		}
	}
}

func TestThinBucketsFallBack(t *testing.T) {
	slowAt14 := func(t time.Time) time.Duration {
		if t.Hour() == 14 {
			return 3000 * ms
		}
		return 1000 * ms
	}
	at := monday.Add(2*24*time.Hour + 14*time.Hour + 10*time.Minute)
	for _, tt := range []struct {
		name   string
		step   time.Duration
		probes int
		p50    time.Duration
	}{
		{"same hour on any weekday", 5 * time.Minute, 36, 3000 * ms}, // 12 a day: thin bucket, 3 days at 14h
		{"whole window", 30 * time.Minute, 144, 1000 * ms},           // 2 a day at 14h: thin hour too
	} {
		now := monday.Add(3 * 24 * time.Hour)
		s, ok := Learn(every(tt.step, monday, now, slowAt14), "standard-v1", 2, now).At(at)
		if !ok || s.Probes != tt.probes || s.TTFT50 != tt.p50 {
			t.Errorf("%s: got %+v ok=%v, want %d probes and p50 %v", tt.name, s, ok, tt.probes, tt.p50)
		}
	}
}

func TestOnlyHealthyProbesOfTheScenarioCount(t *testing.T) {
	var samples []store.Sample
	for i := range 20 {
		samples = append(samples, healthy(fmt.Sprint("ok", i), monday.Add(time.Duration(i)*time.Minute), time.Duration(i+1)*100*ms)...)
	}
	failed := healthy("failed", monday, 9000*ms)
	failed[2].Error, failed[2].TTFT = "overloaded_error: Overloaded", 0
	wrong := healthy("wrong", monday, 9000*ms)
	wrong[1].ExpectOK = false
	other := healthy("other", monday, 9000*ms)
	for i := range other {
		other[i].Scenario = "standard-v0"
	}
	samples = append(samples, append(append(failed, wrong...), other...)...)

	now := monday.Add(25 * time.Hour)
	s, ok := Learn(samples, "standard-v1", 2, now).At(monday)
	if !ok || s.Probes != 20 || s.TTFT50 != 1000*ms || s.TTFT95 != 1900*ms {
		t.Errorf("got %+v ok=%v, want 20 probes, p50 1s, p95 1.9s", s, ok)
	}
}

func TestProbeTTFTIsItsMiddleTurn(t *testing.T) {
	var samples []store.Sample
	for i := range 20 {
		samples = append(samples, withTurns(fmt.Sprint(i), monday.Add(time.Duration(i)*time.Minute), 900*ms, 3000*ms, 1200*ms)...)
	}
	s, _ := Learn(samples, "standard-v1", 2, monday.Add(25*time.Hour)).At(monday)
	if s.TTFT50 != 1200*ms || s.TTFT95 != 1200*ms {
		t.Errorf("got p50 %v p95 %v, want 1.2s for both", s.TTFT50, s.TTFT95)
	}
}

func TestCacheShare(t *testing.T) {
	var samples []store.Sample
	add := func(id string, change func([]store.Sample) []store.Sample) {
		samples = append(samples, change(healthy(id, monday, 1000*ms))...)
	}
	for i := range 17 {
		add(fmt.Sprint("hit", i), func(p []store.Sample) []store.Sample { return p })
	}
	for i := range 3 {
		add(fmt.Sprint("miss", i), func(p []store.Sample) []store.Sample { p[2].CacheReadTokens = 0; return p })
	}
	// Turn 3 failed after turn 2 read the cache: a hit on the turns that completed.
	add("turn3-failed", func(p []store.Sample) []store.Sample { p[2].Error, p[2].CacheReadTokens = "timeout", 0; return p })
	// Stopped at turn 1: says nothing about the cache.
	add("turn1-failed", func(p []store.Sample) []store.Sample {
		p[0].Error, p[0].TTFT = "http 529: overloaded_error: Overloaded", 0
		return p[:1]
	})

	now := monday.Add(25 * time.Hour)
	s, _ := Learn(samples, "standard-v1", 2, now).At(monday)
	if s.Cached != 21 || s.CacheShare != 18.0/21 {
		t.Errorf("got %d cached probes, share %.3f, want 21 and %.3f", s.Cached, s.CacheShare, 18.0/21)
	}
	if s, _ := Learn(samples, "standard-v1", 0, now).At(monday); s.Cached != 0 || s.CacheShare != 0 {
		t.Errorf("scenario without cache turns: got %+v", s)
	}
}
