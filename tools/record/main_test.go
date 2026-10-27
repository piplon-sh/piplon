package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScrub(t *testing.T) {
	in := `{"id":"msg_01AbCdEf","x":"toolu_9Zz","y":"chatcmpl-AbC123","z":"call_xY9","fp":"fp_44709d6fcb","keep":"message_start"}`
	got, n := scrub([]byte(in))
	want := `{"id":"msg_scrubbed","x":"toolu_scrubbed","y":"chatcmpl-scrubbed","z":"call_scrubbed","fp":"fp_scrubbed","keep":"message_start"}`
	if string(got) != want || n != 5 {
		t.Errorf("got %s (%d), want %s (5)", got, n, want)
	}
}

// serveFixtures answers requests in order with the given synthetic files.
func serveFixtures(t *testing.T, provider string, files ...string) string {
	t.Helper()
	var i int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		data, err := os.ReadFile(filepath.Join("..", "..", "internal", "probe", "testdata", provider, files[i])) //nolint:gosec // test fixtures
		if err != nil {
			t.Error(err)
			return
		}
		i++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestRecordWritesScrubbedTurns(t *testing.T) {
	url := serveFixtures(t, "anthropic", "turn1.sse", "turn2.sse", "turn3.sse")
	out := t.TempDir()
	var stdout bytes.Buffer
	env := map[string]string{"ANTHROPIC_API_KEY": "sk-test-key"}
	err := run([]string{"--format", "anthropic", "--model", "m", "--url", url, "--out", out}, func(k string) string { return env[k] }, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"turn1.sse", "turn2.sse", "turn3.sse"} {
		data, err := os.ReadFile(filepath.Join(out, name)) //nolint:gosec // temp dir
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("msg_fixture")) {
			t.Errorf("%s: id not scrubbed", name)
		}
		if !bytes.Contains(data, []byte("message_stop")) {
			t.Errorf("%s: body not recorded in full", name)
		}
	}
	if strings.Contains(stdout.String(), "sk-test-key") {
		t.Error("key printed")
	}
}

func TestRecordWritesNothingWhenATurnFails(t *testing.T) {
	url := serveFixtures(t, "anthropic", "stream_cut.sse")
	out := filepath.Join(t.TempDir(), "recorded")
	env := map[string]string{"ANTHROPIC_API_KEY": "sk-test-key"}
	err := run([]string{"--format", "anthropic", "--model", "m", "--url", url, "--out", out}, func(k string) string { return env[k] }, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "turn 1") {
		t.Fatalf("got %v, want an error naming turn 1", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("output directory created despite the failure")
	}
}

func TestRecordRefusesToWriteTheKey(t *testing.T) {
	// The fixture text "READY" stands in for a key that leaked into a body.
	url := serveFixtures(t, "anthropic", "turn1.sse", "turn2.sse", "turn3.sse")
	out := filepath.Join(t.TempDir(), "recorded")
	env := map[string]string{"ANTHROPIC_API_KEY": "REA"}
	err := run([]string{"--format", "anthropic", "--model", "m", "--url", url, "--out", out}, func(k string) string { return env[k] }, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "contains the key") {
		t.Fatalf("got %v, want a refusal", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("output directory created despite the refusal")
	}
}

func TestRecordNeedsTheKey(t *testing.T) {
	err := run([]string{"--format", "openai", "--model", "m"}, func(string) string { return "" }, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("got %v, want an error naming OPENAI_API_KEY", err)
	}
}

func TestDebugShowsTheFailedTurnBody(t *testing.T) {
	url := serveFixtures(t, "anthropic", "stream_cut.sse")
	out := filepath.Join(t.TempDir(), "recorded")
	var stderr bytes.Buffer
	env := map[string]string{"ANTHROPIC_API_KEY": "sk-test-key"}
	err := run([]string{"--format", "anthropic", "--model", "m", "--url", url, "--out", out, "--debug"},
		func(k string) string { return env[k] }, &bytes.Buffer{}, &stderr)
	if err == nil {
		t.Fatal("expected the failure to be reported")
	}
	got := stderr.String()
	if !strings.Contains(got, "turn 1 raw answer") || !strings.Contains(got, "content_block_delta") {
		t.Errorf("stderr does not show the raw body:\n%s", got)
	}
	if strings.Contains(got, "msg_fixture") || !strings.Contains(got, "msg_scrubbed") {
		t.Errorf("ids not scrubbed in debug output:\n%s", got)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("debug must not write fixtures")
	}
}

func TestDebugNeverShowsTheKey(t *testing.T) {
	url := serveFixtures(t, "anthropic", "stream_cut.sse") // contains "REA"
	var stderr bytes.Buffer
	env := map[string]string{"ANTHROPIC_API_KEY": "REA"}
	_ = run([]string{"--format", "anthropic", "--model", "m", "--url", url, "--out", t.TempDir(), "--debug"},
		func(k string) string { return env[k] }, &bytes.Buffer{}, &stderr)
	if strings.Contains(stderr.String(), "content_block_delta") || !strings.Contains(stderr.String(), "contains the key") {
		t.Errorf("want a refusal instead of the body, got:\n%s", stderr.String())
	}
}

func TestWithoutDebugStderrStaysQuietAndHints(t *testing.T) {
	url := serveFixtures(t, "anthropic", "stream_cut.sse")
	var stderr bytes.Buffer
	env := map[string]string{"ANTHROPIC_API_KEY": "sk-test-key"}
	err := run([]string{"--format", "anthropic", "--model", "m", "--url", url, "--out", t.TempDir()},
		func(k string) string { return env[k] }, &bytes.Buffer{}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--debug") {
		t.Errorf("error %v should suggest --debug", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty without --debug, got:\n%s", stderr.String())
	}
}

func TestDebugWorksForOpenAI(t *testing.T) {
	url := serveFixtures(t, "openai", "stream_cut.sse")
	var stderr bytes.Buffer
	env := map[string]string{"OPENAI_API_KEY": "sk-test-key"}
	err := run([]string{"--format", "openai", "--model", "m", "--url", url, "--out", t.TempDir(), "--debug"},
		func(k string) string { return env[k] }, &bytes.Buffer{}, &stderr)
	if err == nil || !strings.Contains(stderr.String(), "chatcmpl-scrubbed") {
		t.Errorf("want the scrubbed openai body on stderr, got err=%v\n%s", err, stderr.String())
	}
}
