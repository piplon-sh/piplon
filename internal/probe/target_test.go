package probe

import (
	"errors"
	"strings"
	"testing"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/store"
)

func TestForTarget(t *testing.T) {
	a, err := ForTarget(config.Target{Name: "a", Format: "anthropic", URL: "https://a", Model: "m", Key: "k"})
	if got, ok := a.(*Anthropic); err != nil || !ok || got.URL != "https://a" || got.Model != "m" || got.Key != "k" {
		t.Errorf("anthropic: got %#v, %v", a, err)
	}
	o, err := ForTarget(config.Target{Name: "o", Format: "openai", URL: "https://o/v1", Model: "m"})
	if got, ok := o.(*OpenAI); err != nil || !ok || got.URL != "https://o/v1" {
		t.Errorf("openai: got %#v, %v", o, err)
	}
	l, err := ForTarget(config.Target{Name: "l", Format: "ollama", URL: config.OllamaURL, Model: "llama3.2"})
	if got, ok := l.(*OpenAI); err != nil || !ok || got.URL != config.OllamaURL || got.Key != "" {
		t.Errorf("ollama: got %#v, %v, want the OpenAI adapter without a key", l, err)
	}
	for _, f := range []string{"bedrock", "google"} {
		_, err := ForTarget(config.Target{Name: "x", Format: f})
		if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "target x: "+f) {
			t.Errorf("%s: got %v, want ErrUnsupported naming the target and format", f, err)
		}
	}
}

func TestTurnRefused(t *testing.T) {
	for err, want := range map[string]bool{
		"refused": true, "refused: bio": true, "": false, "empty reply": false, "refusedx": false,
	} {
		if got := (Turn{Error: err}).Refused(); got != want {
			t.Errorf("%q: got %v, want %v", err, got, want)
		}
	}
}

func TestSamplesCostMatchesCost(t *testing.T) {
	p := config.Price{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}
	turns := []Turn{
		{InputTokens: 3, OutputTokens: 5, CacheWriteTokens: 2000},
		{InputTokens: 20, OutputTokens: 5, CacheReadTokens: 2000},
	}
	o := Outcome{Target: "t", Turns: turns}
	if got, want := SamplesCost(p, o.Samples()), cost(p, turns); !near(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if SamplesCost(p, []store.Sample(nil)) != 0 {
		t.Error("no samples must cost nothing")
	}
}
