// Command record runs one real probe and saves the provider's raw streamed
// answers as replay fixtures under internal/probe/testdata/<format>/recorded/.
//
// It is a development tool, not part of the piplon binary. It costs one real
// probe (a few cents) and needs a key in the environment:
//
//	ANTHROPIC_API_KEY=... go run ./tools/record --format anthropic --model claude-sonnet-4-5
//	OPENAI_API_KEY=... go run ./tools/record --format openai --model gpt-4o-mini
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
	if err := run(os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "record:", err)
		os.Exit(1)
	}
}

var defaults = map[string]struct{ url, keyEnv string }{
	"anthropic": {"https://api.anthropic.com", "ANTHROPIC_API_KEY"},
	"openai":    {"https://api.openai.com/v1", "OPENAI_API_KEY"},
}

func run(args []string, getenv func(string) string, stdout io.Writer) error {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	format := fs.String("format", "", "anthropic or openai")
	model := fs.String("model", "", "model to probe")
	url := fs.String("url", "", "base URL (default: the provider's public API)")
	keyEnv := fs.String("key-env", "", "environment variable holding the key (default per format)")
	out := fs.String("out", "", "output directory (default internal/probe/testdata/<format>/recorded)")
	timeout := fs.Duration("timeout", time.Minute, "timeout for the whole probe")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, ok := defaults[*format]
	if !ok {
		return fmt.Errorf("--format must be anthropic or openai, got %q", *format)
	}
	if *model == "" {
		return errors.New("--model is required")
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
		p = &probe.Anthropic{URL: *url, Model: *model, Key: config.Secret(key), Client: client}
	case "openai":
		p = &probe.OpenAI{URL: *url, Model: *model, Key: config.Secret(key), Client: client}
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	res := p.Probe(ctx, s)
	for i, t := range res.Turns {
		if t.Error != "" {
			return fmt.Errorf("turn %d failed, nothing written: %s", i+1, t.Error)
		}
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

// ids matches provider-generated identifiers; the prefix is kept.
var ids = regexp.MustCompile(`\b(msg_|toolu_|chatcmpl-|call_|fp_)[A-Za-z0-9_-]+`)

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
