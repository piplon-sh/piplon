package probe

import (
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/scenario"
	"github.com/piplon-sh/piplon/internal/store"
)

func TestCost(t *testing.T) {
	p := config.Price{Input: 3, Output: 15, CacheRead: 0.30, CacheWrite: 3.75}
	turns := []Turn{
		{InputTokens: 100, CacheWriteTokens: 1900, OutputTokens: 3},
		{InputTokens: 120, CacheReadTokens: 1900, OutputTokens: 3},
	}
	// (220*3 + 6*15 + 1900*0.30 + 1900*3.75) / 1e6
	want := (220*3.0 + 6*15.0 + 1900*0.30 + 1900*3.75) / 1e6
	if got := cost(p, turns); !near(got, want) {
		t.Errorf("cost: got %v, want %v", got, want)
	}
}

func TestEstimateIsPricedAsUncached(t *testing.T) {
	s, err := scenario.Load("standard-v1")
	if err != nil {
		t.Fatal(err)
	}
	p := config.Price{Input: 1, Output: 10}
	turns := float64(len(s.Turns))
	want := (turns*float64(s.Limits.InputTokens)*1 + turns*float64(s.Limits.MaxOutputTokens)*10) / 1e6
	if got := estimate(p, s); !near(got, want) {
		t.Errorf("estimate: got %v, want %v", got, want)
	}
}

func TestOutcomeSamples(t *testing.T) {
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	o := Outcome{
		Target: "t", ProbeID: "t-1", Scenario: "standard-v1",
		Turns: []Turn{
			{Start: start, TTFT: time.Second, Total: 2 * time.Second, HTTPStatus: 200, ExpectOK: true, OutputTokens: 3},
			{Start: start.Add(2 * time.Second), HTTPStatus: 529, ProviderError: "overloaded_error", Error: "overloaded"},
		},
	}
	got := o.Samples()
	if len(got) != 2 {
		t.Fatalf("got %d samples, want 2", len(got))
	}
	want := store.Sample{
		ProbeID: "t-1", Time: start.Add(2 * time.Second), Target: "t", Scenario: "standard-v1", Turn: 2,
		HTTPStatus: 529, ProviderError: "overloaded_error", Error: "overloaded",
	}
	if got[1] != want {
		t.Errorf("turn 2:\n got %+v\nwant %+v", got[1], want)
	}
	if got[0].Turn != 1 || got[0].TTFT != time.Second || !got[0].ExpectOK {
		t.Errorf("turn 1: got %+v", got[0])
	}
	if (Outcome{Skipped: SkipBudget}).Samples() != nil {
		t.Error("a skipped probe has no samples")
	}
}
