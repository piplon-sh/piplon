package probe

import (
	"os"
	"path/filepath"
	"strconv"
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
