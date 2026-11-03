package analyze

import (
	"testing"

	"github.com/piplon-sh/piplon/internal/store"
)

func TestSummarize(t *testing.T) {
	r := Summarize([]store.Sample{
		{ProbeID: "a", Turn: 1, TTFT: 965 * ms, ExpectOK: true}, {ProbeID: "a", Turn: 2, TTFT: 1100 * ms, CacheReadTokens: 1900, ExpectOK: true},
		{ProbeID: "b", Turn: 1, TTFT: 2088 * ms, ExpectOK: true}, {ProbeID: "b", Turn: 2, Error: "overloaded_error: Overloaded"},
		{ProbeID: "c", Turn: 1, Error: "refused: bio"},
		{ProbeID: "d", Turn: 1, TTFT: 700 * ms, ExpectOK: true}, {ProbeID: "d", Turn: 2, TTFT: 300 * ms, ExpectOK: true},
	}, 2)
	if r.Probes != 4 || r.ErrPct != 25 || r.TTFT != 965*ms || !r.Cached || r.Cache != "miss" {
		t.Errorf("got %+v, want 4 probes, 25 %% failed (not the refusal), median 965 ms, latest probe missed", r)
	}
}

func TestSummarizeCache(t *testing.T) {
	for _, tt := range []struct {
		name    string
		samples []store.Sample
		want    string
	}{
		{"no probe", nil, "-"},
		{"hit", []store.Sample{{ProbeID: "a", Turn: 1}, {ProbeID: "a", Turn: 2, CacheReadTokens: 1900}}, "hit"},
		{"never cached is not a miss", []store.Sample{{ProbeID: "a", Turn: 1}, {ProbeID: "a", Turn: 2}}, "n/a"},
		{"stopped caching", []store.Sample{{ProbeID: "a", Turn: 2, CacheReadTokens: 1900}, {ProbeID: "b", Turn: 1}, {ProbeID: "b", Turn: 2}}, "miss"},
		{"failed before the cache turn", []store.Sample{{ProbeID: "a", Turn: 1, Error: "timeout"}}, "-"},
	} {
		if got := Summarize(tt.samples, 2).Cache; got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}
