package probe

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
)

func newFakeAPI(t *testing.T, replies ...reply) (*fakeAPI, *Anthropic) {
	t.Helper()
	f, url := newFakeServer(t, "anthropic", replies...)
	return f, &Anthropic{URL: url + "/", Model: "claude-sonnet-4-5", Key: config.Secret(fixtureKey)}
}

func okReplies() []reply {
	return []reply{{status: 200, fixture: "turn1.sse"}, {status: 200, fixture: "turn2.sse"}, {status: 200, fixture: "turn3.sse"}}
}

func TestAnthropicRequestGolden(t *testing.T) {
	f, a := newFakeAPI(t, okReplies()...)
	a.Probe(t.Context(), loadScenario(t))
	if len(f.bodies) != 3 {
		t.Fatalf("got %d requests, want 3", len(f.bodies))
	}
	for i, h := range f.headers {
		if f.paths[i] != "POST /v1/messages" {
			t.Errorf("request %d: got %s, want POST /v1/messages", i+1, f.paths[i])
		}
		for name, want := range map[string]string{
			"X-Api-Key":         fixtureKey,
			"Anthropic-Version": "2023-06-01",
			"Content-Type":      "application/json",
		} {
			if got := h.Get(name); got != want {
				t.Errorf("request %d: header %s = %q, want %q", i+1, name, got, want)
			}
		}
		if !strings.HasPrefix(h.Get("User-Agent"), "piplon/") {
			t.Errorf("request %d: User-Agent %q", i+1, h.Get("User-Agent"))
		}
	}
	for _, g := range []struct {
		turn int
		file string
	}{{1, "request_turn1.golden.json"}, {3, "request_turn3.golden.json"}} {
		checkGolden(t, "anthropic", g.file, f.bodies[g.turn-1])
	}
}

func TestAnthropicReplayOK(t *testing.T) {
	_, a := newFakeAPI(t, okReplies()...)
	res := a.Probe(t.Context(), loadScenario(t))
	if len(res.Turns) != 3 {
		t.Fatalf("got %d turns, want 3: %+v", len(res.Turns), res.Turns)
	}
	want := []struct{ in, read, write int }{{23, 0, 1990}, {31, 1990, 0}, {39, 1990, 0}}
	for i, turn := range res.Turns {
		if turn.Error != "" || turn.StreamCut || !turn.ExpectOK || turn.HTTPStatus != 200 {
			t.Errorf("turn %d: not a clean success: %+v", i+1, turn)
		}
		if turn.InputTokens != want[i].in || turn.CacheReadTokens != want[i].read || turn.CacheWriteTokens != want[i].write {
			t.Errorf("turn %d: tokens in=%d read=%d write=%d, want %+v",
				i+1, turn.InputTokens, turn.CacheReadTokens, turn.CacheWriteTokens, want[i])
		}
		if turn.OutputTokens != 3 {
			t.Errorf("turn %d: output tokens %d, want 3", i+1, turn.OutputTokens)
		}
		if turn.TTFT <= 0 || turn.Total < turn.TTFT || turn.Start.IsZero() {
			t.Errorf("turn %d: timing start=%v ttft=%v total=%v", i+1, turn.Start, turn.TTFT, turn.Total)
		}
	}
}

func TestAnthropicTTFTWaitsForFirstContentDelta(t *testing.T) {
	_, a := newFakeAPI(t,
		reply{status: 200, fixture: "turn1.sse", pause: 80 * time.Millisecond},
		reply{status: 200, fixture: "turn2.sse"}, reply{status: 200, fixture: "turn3.sse"})
	res := a.Probe(t.Context(), loadScenario(t))
	if got := res.Turns[0].TTFT; got < 80*time.Millisecond {
		t.Errorf("TTFT %v counts message_start; want at least the 80ms pause before the first delta", got)
	}
}

func TestAnthropicFailures(t *testing.T) {
	tests := []struct {
		name          string
		replies       []reply
		turns         int
		status        int
		providerError string
		errContains   string
		cut           bool
	}{
		{
			name:    "http 529 stops the conversation",
			replies: []reply{{status: 529, fixture: "overloaded_529.json"}},
			turns:   1, status: 529, providerError: "overloaded_error", errContains: "Overloaded",
		},
		{
			name:    "error event mid-stream",
			replies: []reply{{status: 200, fixture: "stream_error.sse"}},
			turns:   1, status: 200, providerError: "overloaded_error", errContains: "Overloaded",
		},
		{
			name:    "stream ends without message_stop",
			replies: []reply{{status: 200, fixture: "stream_cut.sse"}},
			turns:   1, status: 200, errContains: "stream ended before message_stop", cut: true,
		},
		{
			name:    "model calls a tool instead of answering",
			replies: []reply{{status: 200, fixture: "tool_use.sse"}},
			turns:   1, status: 200, errContains: "tool call instead of text",
		},
		{
			name:    "refusal with a category and no content block",
			replies: []reply{{status: 200, fixture: "refusal_bio.sse"}},
			turns:   1, status: 200, errContains: "refused: bio",
		},
		{
			name:    "second turn fails after a good first",
			replies: []reply{{status: 200, fixture: "turn1.sse"}, {status: 529, fixture: "overloaded_529.json"}},
			turns:   2, status: 529, providerError: "overloaded_error", errContains: "Overloaded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, a := newFakeAPI(t, tt.replies...)
			res := a.Probe(t.Context(), loadScenario(t))
			if len(res.Turns) != tt.turns {
				t.Fatalf("got %d turns, want %d", len(res.Turns), tt.turns)
			}
			last := res.Turns[len(res.Turns)-1]
			if last.HTTPStatus != tt.status || last.ProviderError != tt.providerError || last.StreamCut != tt.cut {
				t.Errorf("last turn: status=%d provider=%q cut=%v, want %d %q %v",
					last.HTTPStatus, last.ProviderError, last.StreamCut, tt.status, tt.providerError, tt.cut)
			}
			if last.ExpectOK || !strings.Contains(last.Error, tt.errContains) {
				t.Errorf("last turn: ExpectOK=%v Error=%q, want false and %q", last.ExpectOK, last.Error, tt.errContains)
			}
		})
	}
}

func TestAnthropicWrongAnswerKeepsGoing(t *testing.T) {
	_, a := newFakeAPI(t, reply{status: 200, fixture: "wrong_answer.sse"},
		reply{status: 200, fixture: "turn2.sse"}, reply{status: 200, fixture: "turn3.sse"})
	res := a.Probe(t.Context(), loadScenario(t))
	if len(res.Turns) != 3 {
		t.Fatalf("got %d turns, want 3: a wrong answer must not stop cache measurement", len(res.Turns))
	}
	if res.Turns[0].ExpectOK || res.Turns[0].Error != "" {
		t.Errorf("turn 1: ExpectOK=%v Error=%q, want false and no error", res.Turns[0].ExpectOK, res.Turns[0].Error)
	}
	if !res.Turns[1].ExpectOK {
		t.Error("turn 2 should pass")
	}
}

func TestAnthropicTimeout(t *testing.T) {
	url := hangingServer(t)
	a := &Anthropic{URL: url, Model: "m", Key: config.Secret(fixtureKey)}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	res := a.Probe(ctx, loadScenario(t))
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Probe returned after %v, want shortly after the 100ms deadline", elapsed)
	}
	if len(res.Turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(res.Turns))
	}
	turn := res.Turns[0]
	if turn.Error != "timeout" || turn.HTTPStatus != 0 || turn.StreamCut || turn.ExpectOK {
		t.Errorf("got %+v, want a timeout with no status", turn)
	}
}

func TestAnthropicNeverLeaksTheKey(t *testing.T) {
	for _, rep := range []reply{{status: 529, fixture: "overloaded_529.json"}, {status: 200, fixture: "stream_cut.sse"}} {
		_, a := newFakeAPI(t, rep)
		res := a.Probe(t.Context(), loadScenario(t))
		for _, turn := range res.Turns {
			if strings.Contains(turn.Error, fixtureKey) || strings.Contains(turn.ProviderError, fixtureKey) {
				t.Errorf("key leaked into %+v", turn)
			}
		}
	}
}
