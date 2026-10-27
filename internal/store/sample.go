package store

import "time"

// Below these, a throughput figure measures network jitter, not generation:
// a one-word reply is 2-3 tokens, often delivered in a single delta.
const (
	minThroughputTokens = 10
	minGeneration       = 50 * time.Millisecond
)

// TokensPerSecond is the generation speed after the first token:
// (OutputTokens - 1) / (Total - TTFT). ok is false when no first token
// arrived, the reply has fewer than 10 tokens, or generation took under
// 50 ms. standard-v1 answers one word per turn, so it rarely yields a value.
func (s Sample) TokensPerSecond() (tps float64, ok bool) {
	gen := s.Total - s.TTFT
	if s.TTFT <= 0 || s.OutputTokens < minThroughputTokens || gen < minGeneration {
		return 0, false
	}
	return float64(s.OutputTokens-1) / gen.Seconds(), true
}
