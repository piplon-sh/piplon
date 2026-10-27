package store

import (
	"testing"
	"time"
)

func TestTokensPerSecond(t *testing.T) {
	tests := []struct {
		name   string
		s      Sample
		want   float64
		wantOK bool
	}{
		{"normal", Sample{OutputTokens: 101, TTFT: 400 * time.Millisecond, Total: 1400 * time.Millisecond}, 100, true},
		{"one-word reply is too short", Sample{OutputTokens: 3, TTFT: 400 * time.Millisecond, Total: 900 * time.Millisecond}, 0, false},
		{"exactly the minimum tokens", Sample{OutputTokens: 10, TTFT: 0 + time.Millisecond, Total: 901 * time.Millisecond}, 10, true},
		{"generation too short to time", Sample{OutputTokens: 50, TTFT: 400 * time.Millisecond, Total: 440 * time.Millisecond}, 0, false},
		{"no first token", Sample{OutputTokens: 50, Total: time.Second}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.s.TokensPerSecond()
			if ok != tt.wantOK || (ok && (got < tt.want-1e-9 || got > tt.want+1e-9)) {
				t.Errorf("got %v, %v; want %v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
