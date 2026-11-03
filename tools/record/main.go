// Command record runs one real probe and saves the provider's raw streamed
// answers as replay fixtures under internal/probe/testdata/<format>/recorded/.
//
// It is a development tool, not part of the piplon binary. It costs one real
// probe (a few cents, nothing on Groq's free plan) and needs a key in the
// environment:
//
//	ANTHROPIC_API_KEY=... go run ./tools/record --format anthropic --model claude-sonnet-4-5
//	OPENAI_API_KEY=... go run ./tools/record --format openai --model gpt-4o-mini
//	GROQ_API_KEY=... go run ./tools/record --format groq --model llama-3.1-8b-instant
//
// groq probes Groq's OpenAI-compatible API with the openai adapter.
//
// --max-output-tokens and --reasoning-effort set what the target options
// max_output_tokens and reasoning_effort set, so a reasoning model can be
// recorded as piplon would probe it:
//
//	go run ./tools/record --format groq --model openai/gpt-oss-20b --reasoning-effort low --max-output-tokens 200
//
// With --save-error FILE, turn 1 is expected to get an HTTP error, such as a
// 404 for a model that does not exist or a 401 for a wrong key, and its body
// is saved as FILE next to the recorded/ directory instead of the turns.
//
// With --debug, a failed turn's raw answer is printed to stderr instead, with
// ids scrubbed and never if it contains the key, so a mismatch between the
// real stream and the adapter can be inspected.
//
// Response headers are never saved, provider ids are replaced, and nothing is
// written if any turn fails or if a body contains the key.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/probe"
	"github.com/piplon-sh/piplon/internal/scenario"
)

func main() {
	if err := run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "record:", err)
		os.Exit(1)
	}
}

var defaults = map[string]struct{ url, keyEnv string }{
	"anthropic": {"https://api.anthropic.com", "ANTHROPIC_API_KEY"},
	"openai":    {"https://api.openai.com/v1", "OPENAI_API_KEY"},
	"groq":      {"https://api.groq.com/openai/v1", "GROQ_API_KEY"},
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	format := fs.String("format", "", "anthropic, openai or groq")
	model := fs.String("model", "", "model to probe")
	url := fs.String("url", "", "base URL (default: the provider's public API)")
	keyEnv := fs.String("key-env", "", "environment variable holding the key (default per format)")
	out := fs.String("out", "", "output directory (default internal/probe/testdata/<format>/recorded)")
	timeout := fs.Duration("timeout", time.Minute, "timeout for the whole probe")
	debug := fs.Bool("debug", false, "on failure, print the failed turn's raw answer (body only, ids scrubbed) to stderr")
	maxOut := fs.Int("max-output-tokens", 0, "replace the scenario's output limit, as max_output_tokens does (1..4096)")
	effort := fs.String("reasoning-effort", "", "send reasoning_effort, as the target option does (openai and groq only)")
	saveError := fs.String("save-error", "", "expect an HTTP error on turn 1 and save its body as this file, next to the output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, ok := defaults[*format]
	if !ok {
		return fmt.Errorf("--format must be anthropic, openai or groq, got %q", *format)
	}
	if *model == "" {
		return errors.New("--model is required")
	}
	switch {
	case *maxOut < 0 || *maxOut > 4096:
		return fmt.Errorf("--max-output-tokens %d is outside 1..4096", *maxOut)
	case *effort != "" && *format == "anthropic":
		return errors.New("--reasoning-effort is only sent by the openai adapter")
	}
	if *saveError != "" && filepath.Base(*saveError) != *saveError {
		return fmt.Errorf("--save-error takes a file name, not a path: %q", *saveError)
	}
	*url = or(*url, d.url)
	*keyEnv = or(*keyEnv, d.keyEnv)
	*out = or(*out, filepath.Join("internal", "probe", "testdata", *format, "recorded"))
	key := getenv(*keyEnv)
	if key == "" {
		return fmt.Errorf("%s is not set", *keyEnv)
	}

	s, err := scenario.Load("standard-v1")
	if err != nil {
		return err
	}
	rec := &recorder{base: http.DefaultTransport}
	client := &http.Client{Transport: rec}
	var p probe.Prober
	switch *format {
	case "anthropic":
		p = &probe.Anthropic{URL: *url, Model: *model, Key: config.Secret(key), Client: client, MaxOutputTokens: *maxOut}
	case "openai", "groq":
		p = &probe.OpenAI{URL: *url, Model: *model, Key: config.Secret(key), Client: client,
			MaxOutputTokens: *maxOut, ReasoningEffort: *effort}
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	res := p.Probe(ctx, s)
	if *saveError != "" {
		return writeError(filepath.Join(filepath.Dir(*out), *saveError), res, rec.bodies(), key, stdout)
	}
	for i, t := range res.Turns {
		if t.Error == "" {
			continue
		}
		if !*debug {
			return fmt.Errorf("turn %d failed, nothing written: %s (rerun with --debug to see the raw answer)", i+1, t.Error)
		}
		if err := showBody(stderr, i, rec.bodies(), key); err != nil {
			return err
		}
		return fmt.Errorf("turn %d failed, nothing written: %s", i+1, t.Error)
	}
	return write(*out, rec.bodies(), key, stdout)
}

// write scrubs every body and saves it as turnN.sse, after checking that no
// body contains the key.
func write(dir string, bodies [][]byte, key string, stdout io.Writer) error {
	scrubbed := make([][]byte, len(bodies))
	total := 0
	for i, b := range bodies {
		if bytes.Contains(b, []byte(key)) {
			return fmt.Errorf("response %d contains the key, nothing written", i+1)
		}
		var n int
		scrubbed[i], n = scrub(b)
		total += n
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	for i, b := range scrubbed {
		path := filepath.Join(dir, "turn"+strconv.Itoa(i+1)+".sse")
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(stdout, "wrote", path)
	}
	_, err := fmt.Fprintf(stdout, "scrubbed %d ids, 0 headers kept\n", total)
	return err
}

// writeError saves the body of turn 1's HTTP error at path, scrubbed, after
// checking that it does not contain the key. A success, or a failure with no
// HTTP answer, writes nothing.
func writeError(path string, res probe.Result, bodies [][]byte, key string, stdout io.Writer) error {
	if len(res.Turns) == 0 || len(bodies) == 0 {
		return errors.New("no answer to turn 1, nothing written")
	}
	t := res.Turns[0]
	if t.HTTPStatus == 0 || t.HTTPStatus == http.StatusOK {
		return fmt.Errorf("turn 1 got no HTTP error (status %d, %q), nothing written", t.HTTPStatus, t.Error)
	}
	if bytes.Contains(bodies[0], []byte(key)) {
		return errors.New("the error body contains the key, nothing written")
	}
	body, n := scrub(bodies[0])
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "wrote %s (HTTP %d: %s)\nscrubbed %d ids, 0 headers kept\n", path, t.HTTPStatus, t.Error, n)
	return err
}

// showBody prints the scrubbed raw body of turn i, unless it holds the key.
// Bodies are all the recorder keeps: headers are never available to print.
func showBody(w io.Writer, i int, bodies [][]byte, key string) error {
	var err error
	switch {
	case i >= len(bodies):
		_, err = fmt.Fprintf(w, "--- turn %d: no response body (the request never got an answer)\n", i+1)
	case bytes.Contains(bodies[i], []byte(key)):
		_, err = fmt.Fprintf(w, "--- turn %d raw answer not shown: it contains the key\n", i+1)
	default:
		body, _ := scrub(bodies[i])
		_, err = fmt.Fprintf(w, "--- turn %d raw answer (body only, ids scrubbed) ---\n%s\n--- end of turn %d ---\n", i+1, body, i+1)
	}
	return err
}

// ids matches provider-generated identifiers; the prefix is kept.
var ids = regexp.MustCompile(`\b(msg_|toolu_|chatcmpl-|call_|fp_|req_)[A-Za-z0-9_-]+`)

// scrub replaces provider ids and reports how many it replaced.
func scrub(b []byte) ([]byte, int) {
	n := 0
	out := ids.ReplaceAllFunc(b, func(m []byte) []byte {
		n++
		prefix := ids.FindSubmatch(m)[1]
		return append(append([]byte{}, prefix...), "scrubbed"...)
	})
	return out, n
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// recorder is an http.RoundTripper that keeps a copy of every response body.
// Only bodies are kept, never headers.
type recorder struct {
	base http.RoundTripper
	mu   sync.Mutex
	got  []*bytes.Buffer
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	buf := &bytes.Buffer{}
	r.mu.Lock()
	r.got = append(r.got, buf)
	r.mu.Unlock()
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.TeeReader(resp.Body, buf), resp.Body}
	return resp, nil
}

func (r *recorder) bodies() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]byte, len(r.got))
	for i, b := range r.got {
		out[i] = b.Bytes()
	}
	return out
}
