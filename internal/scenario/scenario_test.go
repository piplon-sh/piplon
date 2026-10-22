package scenario

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"strings"
	"testing"
	"time"
)

const goldenPath = "testdata/standard-v1.golden.json"

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

func TestLoadStandardV1(t *testing.T) {
	s, err := Load("standard-v1")
	if err != nil {
		t.Fatal(err)
	}
	if s.ID() != "standard-v1" {
		t.Errorf("ID: got %q", s.ID())
	}
	if s.Limits.MaxOutputTokens != 20 || !s.Limits.Stream || s.Limits.Timeout != 30*time.Second {
		t.Errorf("limits: got %+v", s.Limits)
	}
	if len(s.Tools) != 2 || len(s.Turns) != 3 {
		t.Errorf("got %d tools and %d turns, want 2 and 3", len(s.Tools), len(s.Turns))
	}
	if s.Cache.ExpectHitFromTurn != 2 {
		t.Errorf("cache.expect_hit_from_turn: got %d, want 2", s.Cache.ExpectHitFromTurn)
	}
}

// TestGoldenStandardV1 pins the exact bytes every provider receives. If it
// fails, the scenario content changed: that is a new scenario version, not an
// update of this golden file.
func TestGoldenStandardV1(t *testing.T) {
	s, err := Load("standard-v1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Render()
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(goldenPath, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("%v (run go test -update once to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("rendering of standard-v1 changed; content changes need a new scenario version")
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	a, err := Load("standard-v1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load("standard-v1")
	if err != nil {
		t.Fatal(err)
	}
	ra, _ := a.Render()
	rb, _ := b.Render()
	if !bytes.Equal(ra, rb) {
		t.Fatal("two loads rendered different bytes")
	}
}

func TestPaddingSize(t *testing.T) {
	s, err := Load("standard-v1")
	if err != nil {
		t.Fatal(err)
	}
	words := len(strings.Fields(s.PaddingText()))
	estimate := estimateTokens(s.baseText()) + words
	if estimate != s.Padding.TargetInputTokens {
		t.Errorf("estimated turn-1 tokens: got %d, want %d", estimate, s.Padding.TargetInputTokens)
	}
	if !strings.HasPrefix(s.SystemPrompt(), strings.TrimRight(s.System, "\n")+"\n\n") {
		t.Error("system prompt must start with the scenario system text and a blank line")
	}
	if !strings.HasSuffix(s.SystemPrompt(), s.PaddingText()) {
		t.Error("system prompt must end with the padding")
	}
}

func TestPadding(t *testing.T) {
	a := padding("seed-a", 100)
	if got := len(strings.Fields(a)); got != 100 {
		t.Fatalf("got %d words, want 100", got)
	}
	if a != padding("seed-a", 100) {
		t.Error("same seed must give the same padding")
	}
	if a == padding("seed-b", 100) {
		t.Error("different seeds must give different padding")
	}
	if !strings.HasPrefix(padding("seed-a", 200), a) {
		t.Error("a longer padding must extend a shorter one with the same seed")
	}
	for i, line := range strings.Split(a, "\n") {
		if n := len(strings.Fields(line)); n > wordsPerLine {
			t.Errorf("line %d has %d words, want at most %d", i, n, wordsPerLine)
		}
	}
	if padding("seed-a", 0) != "" {
		t.Error("zero words must give an empty padding")
	}
}

func TestWordList(t *testing.T) {
	if len(words) != 256 {
		t.Fatalf("got %d words, want exactly 256 (one per byte value)", len(words))
	}
	seen := make(map[string]bool, len(words))
	for _, w := range words {
		if seen[w] {
			t.Errorf("duplicate word %q", w)
		}
		seen[w] = true
		if w == "" || strings.ToLower(w) != w || strings.ContainsAny(w, " \n\t") {
			t.Errorf("word %q must be a single lower-case token", w)
		}
	}
}

func TestNames(t *testing.T) {
	names := Names()
	if len(names) == 0 || names[0] != "standard-v1" {
		t.Errorf("got %v, want standard-v1 listed", names)
	}
}

func TestLoadUnknown(t *testing.T) {
	_, err := Load("standard-v9")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "standard-v1") {
		t.Errorf("error %q should list the available scenarios", err)
	}
}

const valid = `
name: t
version: 1
limits: {max_output_tokens: 20, timeout: 30s, stream: true}
system: be terse
tools:
  - name: get_time
    description: time
    parameters: {type: object, properties: {}}
padding: {target_input_tokens: 500, seed: s}
turns:
  - role: user
    content: hi
`

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name, yaml, wantMsg string
	}{
		{"unknown field", valid + "colour: red\n", "field colour not found"},
		{"missing name", strings.Replace(valid, "name: t\n", "", 1), "name: is required"},
		{"version zero", strings.Replace(valid, "version: 1", "version: 0", 1), "version: must be at least 1"},
		{"no system", strings.Replace(valid, "system: be terse\n", "", 1), "system: is required"},
		{"no turns", strings.Split(valid, "turns:")[0], "turns: at least one turn is required"},
		{"assistant turn", strings.Replace(valid, "role: user", "role: assistant", 1), `turns[0].role: "assistant" must be user`},
		{"empty turn", strings.Replace(valid, "content: hi", "content: \"\"", 1), "turns[0].content: is required"},
		{"no output tokens", strings.Replace(valid, "max_output_tokens: 20", "max_output_tokens: 0", 1), "limits.max_output_tokens: must be at least 1"},
		{"no timeout", strings.Replace(valid, "timeout: 30s", "timeout: 0s", 1), "limits.timeout: must be greater than 0"},
		{"no seed", strings.Replace(valid, "seed: s", `seed: ""`, 1), "padding.seed: is required"},
		{"target too small", strings.Replace(valid, "target_input_tokens: 500", "target_input_tokens: 5", 1), "padding.target_input_tokens: 5 is below the"},
		{"duplicate tool", strings.Replace(valid, "padding:", "  - name: get_time\n    description: again\n    parameters: {type: object}\npadding:", 1), `tools[1].name: duplicate "get_time"`},
		{"cache turn out of range", valid + "cache: {expect_hit_from_turn: 4}\n", "cache.expect_hit_from_turn: 4 is outside 0..1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q\ndoes not contain %q", err, tt.wantMsg)
			}
		})
	}
}

func TestParseValid(t *testing.T) {
	s, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if s.ID() != "t-v1" {
		t.Errorf("ID: got %q", s.ID())
	}
}
