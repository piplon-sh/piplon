package probe

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/piplon-sh/piplon/internal/config"
)

// recordedReplies lists testdata/<provider>/recorded/turnN.sse, or skips the
// test when nothing was recorded yet.
func recordedReplies(t *testing.T, provider string) []reply {
	t.Helper()
	var replies []reply
	for i := 1; ; i++ {
		name := filepath.Join("recorded", "turn"+strconv.Itoa(i)+".sse")
		if _, err := os.Stat(filepath.Join("testdata", provider, name)); err != nil {
			break
		}
		replies = append(replies, reply{status: 200, fixture: name})
	}
	if len(replies) == 0 {
		t.Skipf("no recordings in testdata/%s/recorded; run: go run ./tools/record --format %s --model <model>", provider, provider)
	}
	return replies
}

// checkRecorded asserts what any real, successful probe must show. Exact
// numbers depend on the recording, so they are logged, not pinned.
func checkRecorded(t *testing.T, res Result, want int) {
	t.Helper()
	if len(res.Turns) != want {
		t.Fatalf("got %d turns, want %d", len(res.Turns), want)
	}
	for i, turn := range res.Turns {
		if turn.Error != "" || turn.StreamCut || turn.HTTPStatus != 200 {
			t.Errorf("turn %d failed on a recorded success: %+v", i+1, turn)
		}
		if turn.InputTokens+turn.CacheReadTokens+turn.CacheWriteTokens == 0 || turn.OutputTokens == 0 {
			t.Errorf("turn %d: no usage parsed from the recording: %+v", i+1, turn)
		}
		t.Logf("turn %d: expect_ok=%v in=%d out=%d cache_read=%d cache_write=%d",
			i+1, turn.ExpectOK, turn.InputTokens, turn.OutputTokens, turn.CacheReadTokens, turn.CacheWriteTokens)
	}
}

func TestAnthropicRecorded(t *testing.T) {
	replies := recordedReplies(t, "anthropic")
	_, url := newFakeServer(t, "anthropic", replies...)
	a := &Anthropic{URL: url, Model: "recorded", Key: config.Secret("k")}
	checkRecorded(t, a.Probe(t.Context(), loadScenario(t)), len(replies))
}

func TestOpenAIRecorded(t *testing.T) {
	replies := recordedReplies(t, "openai")
	_, url := newFakeServer(t, "openai", replies...)
	o := &OpenAI{URL: url, Model: "recorded", Key: config.Secret("k")}
	checkRecorded(t, o.Probe(t.Context(), loadScenario(t)), len(replies))
}

// TestGroqRecorded replays Groq's OpenAI-compatible answers through the
// openai adapter.
func TestGroqRecorded(t *testing.T) {
	replies := recordedReplies(t, "groq")
	_, url := newFakeServer(t, "groq", replies...)
	o := &OpenAI{URL: url, Model: "recorded", Key: config.Secret("k")}
	checkRecorded(t, o.Probe(t.Context(), loadScenario(t)), len(replies))
}

// TestGroqRecordedErrors replays real error answers: a model that does not
// exist, which piplon judges gone, and a wrong key. Each case is skipped
// until it is recorded.
func TestGroqRecordedErrors(t *testing.T) {
	for _, tt := range []struct {
		file   string
		status int
	}{
		{"model_not_found_404.json", 404},
		{"invalid_key_401.json", 401},
	} {
		t.Run(tt.file, func(t *testing.T) {
			if _, err := os.Stat(filepath.Join("testdata", "groq", tt.file)); err != nil {
				t.Skipf("not recorded; run: go run ./tools/record --format groq ... --save-error %s", tt.file)
			}
			_, url := newFakeServer(t, "groq", reply{status: tt.status, fixture: tt.file})
			o := &OpenAI{URL: url, Model: "recorded", Key: config.Secret("gsk_recorded_key")}
			res := o.Probe(t.Context(), loadScenario(t))
			if len(res.Turns) != 1 {
				t.Fatalf("got %d turns, want the conversation to stop at turn 1", len(res.Turns))
			}
			turn := res.Turns[0]
			want := "http " + strconv.Itoa(tt.status) + ": "
			if turn.HTTPStatus != tt.status || turn.ProviderError == "" || !strings.HasPrefix(turn.Error, want) || turn.ExpectOK {
				t.Errorf("got %+v, want status %d with the provider's error named", turn, tt.status)
			}
			t.Logf("provider_error=%q error=%q", turn.ProviderError, turn.Error)
		})
	}
}
