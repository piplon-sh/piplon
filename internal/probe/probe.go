package probe

import (
	"context"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/scenario"
	"github.com/piplon-sh/piplon/internal/store"
)

// Prober runs one scenario against one target. It honors ctx and records
// every failure in the Result instead of returning an error.
type Prober interface {
	Probe(ctx context.Context, s *scenario.Scenario) Result
}

// Result is what one probe measured.
type Result struct {
	Turns []Turn // one per attempted turn; stops at the first failed turn
}

// Turn is the measurement of one conversation turn.
type Turn struct {
	Start            time.Time
	TTFT             time.Duration // 0 when no content delta arrived
	Total            time.Duration
	InputTokens      int // uncached input only
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	HTTPStatus       int // 0 when no HTTP response arrived
	ProviderError    string
	Error            string
	StreamCut        bool
	ExpectOK         bool
}

// SkipReason says why a tick did not send a probe; empty means it did.
type SkipReason string

// Reasons a tick is skipped.
const (
	SkipOverlap SkipReason = "overlap" // the previous probe of this target was still pending
	SkipBudget  SkipReason = "budget"  // the daily budget cannot cover the estimate
)

// Outcome is what one tick of one target produced.
type Outcome struct {
	Target   string
	ProbeID  string
	Scenario string
	Start    time.Time // when the tick fired
	Skipped  SkipReason
	Turns    []Turn
	CostUSD  float64
}

// Samples converts the outcome to store rows, one per turn. A skipped tick
// has none.
func (o Outcome) Samples() []store.Sample {
	if len(o.Turns) == 0 {
		return nil
	}
	out := make([]store.Sample, len(o.Turns))
	for i, t := range o.Turns {
		out[i] = store.Sample{
			ProbeID:          o.ProbeID,
			Time:             t.Start,
			Target:           o.Target,
			Scenario:         o.Scenario,
			Turn:             i + 1,
			TTFT:             t.TTFT,
			Total:            t.Total,
			InputTokens:      t.InputTokens,
			OutputTokens:     t.OutputTokens,
			HTTPStatus:       t.HTTPStatus,
			ProviderError:    t.ProviderError,
			Error:            t.Error,
			StreamCut:        t.StreamCut,
			CacheReadTokens:  t.CacheReadTokens,
			CacheWriteTokens: t.CacheWriteTokens,
			ExpectOK:         t.ExpectOK,
		}
	}
	return out
}

const perMillion = 1e6

// cost is what turns actually cost at price p.
func cost(p config.Price, turns []Turn) float64 {
	var usd float64
	for _, t := range turns {
		usd += float64(t.InputTokens)*p.Input +
			float64(t.OutputTokens)*p.Output +
			float64(t.CacheReadTokens)*p.CacheRead +
			float64(t.CacheWriteTokens)*p.CacheWrite
	}
	return usd / perMillion
}

// estimate is the worst-case cost of one probe of s: every turn at full
// measured input size, uncached, with maximum output.
func estimate(p config.Price, s *scenario.Scenario) float64 {
	turns := float64(len(s.Turns))
	in := turns * float64(s.Limits.InputTokens)
	out := turns * float64(s.Limits.MaxOutputTokens)
	return (in*p.Input + out*p.Output) / perMillion
}
