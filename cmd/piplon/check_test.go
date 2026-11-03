package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/analyze"
	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/probe"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

// anthropicServer answers standard-v1 from the probe fixtures, turn after
// turn, as many probes as it is sent. mode changes the answer: reject (401),
// nocache (turns 2 and 3 read no cache), slow (30 ms before answering), or
// flaky (529 on the first request, then like ok).
func anthropicServer(t *testing.T, mode string) string {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i := n.Add(1)
		switch {
		case mode == "reject":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
			return
		case mode == "flaky" && i == 1:
			w.WriteHeader(529)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
			return
		case mode == "flaky":
			i--
		case mode == "slow":
			time.Sleep(30 * time.Millisecond)
		}
		turn := (i-1)%3 + 1
		data, err := os.ReadFile(filepath.Join("..", "..", "internal", "probe", "testdata", "anthropic", fmt.Sprintf("turn%d.sse", turn)))
		if err != nil {
			t.Error(err)
			return
		}
		if mode == "nocache" {
			data = []byte(strings.ReplaceAll(string(data), `"cache_read_input_tokens":1990`, `"cache_read_input_tokens":0`))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// checkConfig writes a config with one anthropic target per name, each
// probing the server url maps it to, and returns its path.
func checkConfig(t *testing.T, urls map[string]string, names ...string) string {
	t.Helper()
	t.Setenv("PIPLON_TEST_KEY", "sk-test-key-for-check")
	cfg := "interval: 10s\njitter: 0s\ntimeout: 5s\ntargets:\n"
	for _, name := range names {
		key := "${PIPLON_TEST_KEY}"
		if strings.HasPrefix(name, "unset") {
			key = "${PIPLON_UNSET_KEY}"
		}
		cfg += fmt.Sprintf("  - name: %s\n    format: anthropic\n    url: %s\n    model: claude-sonnet-4-5\n    key: %s\n", name, urls[name], key)
	}
	path := filepath.Join(t.TempDir(), "piplon.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// exitCode is the code main would exit with for err.
func exitCode(err error) int {
	var exit *exitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.code
	default:
		return 1
	}
}

func TestCheckExitCodes(t *testing.T) {
	retryDelay = 0
	ok, reject, slow, nocache, flaky := anthropicServer(t, "ok"), anthropicServer(t, "reject"),
		anthropicServer(t, "slow"), anthropicServer(t, "nocache"), anthropicServer(t, "flaky")
	for _, tt := range []struct {
		name  string
		urls  map[string]string
		args  []string
		code  int
		wants []string
	}{
		{"passes", map[string]string{"a": ok}, nil, 0, []string{"1 target passed", "a  ", "pass", " · exit 0"}},
		{"a hard failure fails", map[string]string{"a": reject}, nil, 1,
			[]string{"FAIL", "down", "the key was rejected · turn 1: http 401", "1 of 1 target failed", "exit 1"}},
		{"within --max-errors", map[string]string{"a": ok, "b": reject}, []string{"--max-errors", "1"}, 0,
			[]string{"1 hard failure tolerated", "1 of 2 targets failed, within --max-errors", "exit 0"}},
		{"above --max-ttft", map[string]string{"a": slow}, []string{"--max-ttft", "10ms"}, 1,
			[]string{"max TTFT 10ms", "ok", "above 10 ms"}},
		{"below --max-ttft", map[string]string{"a": ok}, []string{"--max-ttft", "5s"}, 0, []string{"pass"}},
		{"--require-cache and a miss", map[string]string{"a": nocache}, []string{"--require-cache"}, 1,
			[]string{"cache required", "miss", "cache not read"}},
		{"--require-cache and a hit", map[string]string{"a": ok}, []string{"--require-cache"}, 0, []string{"hit"}},
		{"--retries saves a 529", map[string]string{"a": flaky}, []string{"--retries", "1"}, 0, []string{"1 retry", "(2 attempts)", "pass"}},
		{"no retry, a 529 fails", map[string]string{"a": anthropicServer(t, "flaky")}, nil, 1, []string{"overloaded"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var names []string
			for _, n := range []string{"a", "b"} {
				if tt.urls[n] != "" {
					names = append(names, n)
				}
			}
			out, err := execute(t, append([]string{"check", "-c", checkConfig(t, tt.urls, names...)}, tt.args...)...)
			if code := exitCode(err); code != tt.code {
				t.Fatalf("exit %d, want %d (%v):\n%s", code, tt.code, err, out)
			}
			for _, want := range tt.wants {
				if !strings.Contains(out, want) {
					t.Errorf("output does not contain %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestCheckCannotJudge(t *testing.T) {
	ok := anthropicServer(t, "ok")
	path := checkConfig(t, map[string]string{"a": ok}, "a")
	bedrock := filepath.Join(t.TempDir(), "bedrock.yaml")
	if err := os.WriteFile(bedrock, []byte("targets:\n  - name: b\n    format: bedrock\n    region: eu-west-1\n    model: m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"missing config", []string{"-c", filepath.Join(t.TempDir(), "nope.yaml")}, "nope.yaml"},
		{"unknown target", []string{"-c", path, "--target", "nope"}, "target nope: name: is not in the config"},
		{"bad flag value", []string{"-c", path, "--max-ttft", "fast"}, "max-ttft"},
		{"unknown flag", []string{"-c", path, "--max-slow", "1s"}, "max-slow"},
		{"negative threshold", []string{"-c", path, "--max-errors", "-1"}, "--max-errors must not be negative"},
		{"unsupported format", []string{"-c", bedrock}, "target b"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := execute(t, append([]string{"check"}, tt.args...)...)
			if code := exitCode(err); code != 2 || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("exit %d (%v), want 2 naming %q", code, err, tt.want)
			}
		})
	}
}

func TestCheckTargetNeedsOnlyItsOwnKey(t *testing.T) {
	ok := anthropicServer(t, "ok")
	path := checkConfig(t, map[string]string{"a": ok, "unset-key": ok}, "a", "unset-key")
	if _, err := execute(t, "check", "-c", path); exitCode(err) != 2 || !strings.Contains(err.Error(), "PIPLON_UNSET_KEY") {
		t.Fatalf("without --target: got %v, want the unset variable named", err)
	}
	out, err := execute(t, "check", "-c", path, "--target", "a")
	if err != nil || !strings.Contains(out, "1 target passed") {
		t.Errorf("with --target a: got %v:\n%s", err, out)
	}
}

func TestCheckJSON(t *testing.T) {
	ok, reject := anthropicServer(t, "ok"), anthropicServer(t, "reject")
	path := checkConfig(t, map[string]string{"a": ok, "b": reject}, "a", "b")
	out, err := execute(t, "check", "-c", path, "--json", "--max-ttft", "5s")
	if exitCode(err) != 1 {
		t.Fatalf("exit %d, want 1: %v", exitCode(err), err)
	}
	var doc checkDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON (%v):\n%s", err, out)
	}
	if doc.Schema != checkSchema || doc.Passed || doc.ExitCode != 1 || len(doc.Targets) != 2 ||
		*doc.Thresholds.MaxTTFTSeconds != 5 {
		t.Fatalf("got %+v", doc)
	}
	a, b := doc.Targets[0], doc.Targets[1]
	if !a.Passed || a.State != "ok" || a.TTFTSeconds == nil || *a.Cache != "hit" || len(a.Turns) != 3 || a.Attempts != 1 {
		t.Errorf("a: %+v", a)
	}
	if b.Passed || b.State != "down" || b.TTFTSeconds != nil || len(b.Failures) != 1 || *b.Turns[0].HTTPStatus != 401 {
		t.Errorf("b: %+v", b)
	}
	for _, secret := range []string{"sk-test-key-for-check", "127.0.0.1"} {
		if strings.Contains(out, secret) {
			t.Errorf("the JSON contains %q", secret)
		}
	}
}

func TestCheckJSONOnAPassThatCannotBeJudged(t *testing.T) {
	out, err := execute(t, "check", "-c", filepath.Join(t.TempDir(), "nope.yaml"), "--json")
	var doc checkErrorDoc
	if exitCode(err) != 2 || json.Unmarshal([]byte(out), &doc) != nil || doc.ExitCode != 2 || !strings.Contains(doc.Error, "nope.yaml") {
		t.Errorf("exit %d, output:\n%s", exitCode(err), out)
	}
}

func TestCheckInterrupted(t *testing.T) {
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(hang.Close)
	t.Cleanup(func() { close(release) }) // runs first: Close waits for the handler
	path := checkConfig(t, map[string]string{"a": hang.URL}, "a")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	var out strings.Builder
	err := runCheck(ctx, checkOptions{config: path}, &out)
	if exitCode(err) != 2 || !errors.Is(err, errInterrupted) {
		t.Errorf("got %v, want exit 2: interrupted, not judged down", err)
	}
	if out.Len() != 0 {
		t.Errorf("an interrupted pass printed a result:\n%s", out.String())
	}
}

// goldenReport is a judged pass with fixed figures: one target passed, one
// too slow, one down.
func goldenReport() *checkReport {
	at := time.Date(2026, 11, 3, 9, 10, 0, 0, time.UTC)
	target := func(name string) config.Target {
		return config.Target{Name: name, Format: "anthropic", Model: "claude-sonnet-4-5", Key: "sk-secret", URL: "https://secret"}
	}
	turn := func(ttft time.Duration, cacheRead int) probe.Turn {
		return probe.Turn{Start: at, TTFT: ttft, Total: ttft + 300*time.Millisecond, InputTokens: 10, OutputTokens: 3,
			CacheReadTokens: cacheRead, HTTPStatus: 200, ExpectOK: true}
	}
	ok := []probe.Turn{turn(412*time.Millisecond, 0), turn(380*time.Millisecond, 1990), turn(390*time.Millisecond, 1990)}
	slow := []probe.Turn{turn(2840*time.Millisecond, 0), turn(2700*time.Millisecond, 1990), turn(2900*time.Millisecond, 1990)}
	down := []probe.Turn{{Start: at, Total: 183 * time.Millisecond, HTTPStatus: 401, ProviderError: "authentication_error",
		Error: "http 401: authentication_error: invalid x-api-key"}}
	r := &checkReport{opts: checkOptions{maxTTFT: 2 * time.Second, requireCache: true}, scenario: "standard-v1",
		started: at, finished: at.Add(4 * time.Second)}
	for _, x := range []struct {
		name  string
		turns []probe.Turn
		cost  float64
	}{{"anthropic-direct", ok, 0.0071}, {"anthropic-gateway", slow, 0.0071}, {"openai-direct", down, 0}} {
		o := probe.Outcome{Target: x.name, Turns: x.turns}
		v := analyze.Classify(o, analyze.Stats{}, false, 2)
		res := targetResult{target: target(x.name), turns: x.turns, verdict: v, attempts: 1, costUSD: x.cost}
		if !v.State.Hard() {
			res.ttft = analyze.TTFT(x.turns)
		}
		r.results = append(r.results, res)
	}
	r.judge()
	return r
}

func TestCheckOutputGolden(t *testing.T) {
	r := goldenReport()
	var text strings.Builder
	if err := r.text(&text); err != nil {
		t.Fatal(err)
	}
	var js strings.Builder
	if err := writeCheckJSON(&js, r.doc()); err != nil {
		t.Fatal(err)
	}
	for file, got := range map[string]string{"check.golden.txt": text.String(), "check.golden.json": js.String()} {
		path := filepath.Join("testdata", file)
		if *update {
			if err := os.MkdirAll("testdata", 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path) //nolint:gosec // a fixed test file
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("%s differs (run with -update to accept):\n%s", path, got)
		}
		if strings.Contains(got, "sk-secret") || strings.Contains(got, "https://secret") {
			t.Errorf("%s contains the key or the URL", file)
		}
	}
}
