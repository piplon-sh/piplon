package probe

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
)

const openaiFixtureKey = "sk-openai-fixture-not-a-real-key"

func newFakeOpenAI(t *testing.T, key string, replies ...reply) (*fakeAPI, *OpenAI) {
	t.Helper()
	f, url := newFakeServer(t, "openai", replies...)
	return f, &OpenAI{URL: url + "/v1/", Model: "gpt-fixture", Key: config.Secret(key)}
}

func openaiOK() []reply {
	return []reply{{status: 200, fixture: "turn1.sse"}, {status: 200, fixture: "turn2.sse"}, {status: 200, fixture: "turn3.sse"}}
}

func TestOpenAIRequestGolden(t *testing.T) {
	f, o := newFakeOpenAI(t, openaiFixtureKey, openaiOK()...)
	o.Probe(t.Context(), loadScenario(t))
	if len(f.bodies) != 3 {
		t.Fatalf("got %d requests, want 3", len(f.bodies))
	}
	for i, h := range f.headers {
		if f.paths[i] != "POST /v1/chat/completions" {
			t.Errorf("request %d: got %s, want POST /v1/chat/completions", i+1, f.paths[i])
		}
		if got := h.Get("Authorization"); got != "Bearer "+openaiFixtureKey {
			t.Errorf("request %d: Authorization %q", i+1, got)
		}
		if got := h.Get("Content-Type"); got != "application/json" {
			t.Errorf("request %d: Content-Type %q", i+1, got)
		}
		if !strings.HasPrefix(h.Get("User-Agent"), "piplon/") {
			t.Errorf("request %d: User-Agent %q", i+1, h.Get("User-Agent"))
		}
	}
	checkGolden(t, "openai", "request_turn1.golden.json", f.bodies[0])
	checkGolden(t, "openai", "request_turn3.golden.json", f.bodies[2])
}

func TestOpenAIWithoutKeySendsNoAuthorization(t *testing.T) {
	f, o := newFakeOpenAI(t, "", openaiOK()...)
	o.Probe(t.Context(), loadScenario(t))
	for i, h := range f.headers {
		if _, ok := h["Authorization"]; ok {
			t.Errorf("request %d: Authorization header sent without a key", i+1)
		}
	}
}

func TestOpenAIReplayOK(t *testing.T) {
	_, o := newFakeOpenAI(t, openaiFixtureKey, openaiOK()...)
	res := o.Probe(t.Context(), loadScenario(t))
	if len(res.Turns) != 3 {
		t.Fatalf("got %d turns, want 3: %+v", len(res.Turns), res.Turns)
	}
	// prompt_tokens includes cached ones; InputTokens must not.
	want := []struct{ in, read int }{{2013, 0}, {2021 - 1920, 1920}, {2029 - 1920, 1920}}
	for i, turn := range res.Turns {
		if turn.Error != "" || turn.StreamCut || !turn.ExpectOK || turn.HTTPStatus != 200 {
			t.Errorf("turn %d: not a clean success: %+v", i+1, turn)
		}
		if turn.InputTokens != want[i].in || turn.CacheReadTokens != want[i].read || turn.CacheWriteTokens != 0 {
			t.Errorf("turn %d: tokens in=%d read=%d write=%d, want %+v",
				i+1, turn.InputTokens, turn.CacheReadTokens, turn.CacheWriteTokens, want[i])
		}
		if turn.OutputTokens != 3 {
			t.Errorf("turn %d: output tokens %d, want 3", i+1, turn.OutputTokens)
		}
		if turn.TTFT <= 0 || turn.Total < turn.TTFT {
			t.Errorf("turn %d: ttft=%v total=%v", i+1, turn.TTFT, turn.Total)
		}
	}
}

func TestOpenAITTFTSkipsTheRoleOnlyChunk(t *testing.T) {
	_, o := newFakeOpenAI(t, openaiFixtureKey,
		reply{status: 200, fixture: "turn1.sse", pause: 80 * time.Millisecond},
		reply{status: 200, fixture: "turn2.sse"}, reply{status: 200, fixture: "turn3.sse"})
	res := o.Probe(t.Context(), loadScenario(t))
	if got := res.Turns[0].TTFT; got < 80*time.Millisecond {
		t.Errorf("TTFT %v counts the empty role chunk; want at least the 80ms pause before content", got)
	}
}

func TestOpenAIFailures(t *testing.T) {
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
			name:    "http 429 prefers the error code",
			replies: []reply{{status: 429, fixture: "rate_limited_429.json"}},
			turns:   1, status: 429, providerError: "rate_limit_exceeded", errContains: "Rate limit reached",
		},
		{
			name:    "error chunk mid-stream falls back to the error type",
			replies: []reply{{status: 200, fixture: "stream_error.sse"}},
			turns:   1, status: 200, providerError: "server_error", errContains: "had an error",
		},
		{
			name:    "model calls a tool instead of answering",
			replies: []reply{{status: 200, fixture: "tool_call.sse"}},
			turns:   1, status: 200, errContains: "tool call instead of text",
		},
		{
			name:    "stream ends without [DONE]",
			replies: []reply{{status: 200, fixture: "stream_cut.sse"}},
			turns:   1, status: 200, errContains: "stream ended before [DONE]", cut: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, o := newFakeOpenAI(t, openaiFixtureKey, tt.replies...)
			res := o.Probe(t.Context(), loadScenario(t))
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
			if strings.Contains(last.Error, openaiFixtureKey) {
				t.Errorf("key leaked into %q", last.Error)
			}
		})
	}
}

func TestOpenAIWrongAnswerKeepsGoing(t *testing.T) {
	_, o := newFakeOpenAI(t, openaiFixtureKey, reply{status: 200, fixture: "wrong_answer.sse"},
		reply{status: 200, fixture: "turn2.sse"}, reply{status: 200, fixture: "turn3.sse"})
	res := o.Probe(t.Context(), loadScenario(t))
	if len(res.Turns) != 3 || res.Turns[0].ExpectOK || res.Turns[0].Error != "" || !res.Turns[1].ExpectOK {
		t.Fatalf("want 3 turns with only turn 1 failing its expectation, got %+v", res.Turns)
	}
}

func TestOpenAITimeout(t *testing.T) {
	o := &OpenAI{URL: hangingServer(t), Model: "m"}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	res := o.Probe(ctx, loadScenario(t))
	if len(res.Turns) != 1 || res.Turns[0].Error != "timeout" || res.Turns[0].HTTPStatus != 0 {
		t.Fatalf("want one timed-out turn with no status, got %+v", res.Turns)
	}
}
