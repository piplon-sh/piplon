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
	err := run([]string{"--format", "anthropic", "--model", "m", "--url", url, "--out", out}, func(k string) string { return env[k] }, &stdout)
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
	err := run([]string{"--format", "anthropic", "--model", "m", "--url", url, "--out", out}, func(k string) string { return env[k] }, &bytes.Buffer{})
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
	err := run([]string{"--format", "anthropic", "--model", "m", "--url", url, "--out", out}, func(k string) string { return env[k] }, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "contains the key") {
		t.Fatalf("got %v, want a refusal", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("output directory created despite the refusal")
	}
}

func TestRecordNeedsTheKey(t *testing.T) {
	err := run([]string{"--format", "openai", "--model", "m"}, func(string) string { return "" }, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("got %v, want an error naming OPENAI_API_KEY", err)
	}
}
