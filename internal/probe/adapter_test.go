package probe

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

const fixtureKey = "sk-ant-fixture-not-a-real-key"

// reply is one canned HTTP response.
type reply struct {
	status  int
	fixture string        // file under testdata/<provider>/
	pause   time.Duration // wait after the first event before sending the rest
}

// fakeAPI serves replies in order and records every request it receives.
type fakeAPI struct {
	t       *testing.T
	dir     string
	mu      sync.Mutex
	replies []reply
	bodies  [][]byte
	headers []http.Header
	paths   []string
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	n := len(f.bodies)
	f.bodies = append(f.bodies, body)
	f.headers = append(f.headers, r.Header.Clone())
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	if n >= len(f.replies) {
		f.t.Errorf("unexpected request %d", n+1)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	rep := f.replies[n]
	data, err := os.ReadFile(filepath.Join("testdata", f.dir, rep.fixture))
	if err != nil {
		f.t.Error(err)
		return
	}
	if rep.status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.status)
		_, _ = w.Write(data)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	first, rest, _ := bytes.Cut(data, []byte("\n\n"))
	_, _ = w.Write(append(first, '\n', '\n'))
	w.(http.Flusher).Flush()
	time.Sleep(rep.pause)
	_, _ = w.Write(rest)
}

// newFakeServer serves replies from testdata/<dir>/ and returns its base URL.
func newFakeServer(t *testing.T, dir string, replies ...reply) (*fakeAPI, string) {
	t.Helper()
	f := &fakeAPI{t: t, dir: dir, replies: replies}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

// hangingServer accepts requests and never answers.
func hangingServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// Reading the body lets the server notice when the client hangs up,
		// which also proves the adapter closes the connection on timeout.
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done() // never answers
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// checkGolden compares an indented request body with testdata/<dir>/<file>.
func checkGolden(t *testing.T, dir, file string, body []byte) {
	t.Helper()
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	path := filepath.Join("testdata", dir, file)
	if *update {
		if err := os.WriteFile(path, pretty.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // fixed test file names
	if err != nil {
		t.Fatalf("%v (run go test -update once to create it)", err)
	}
	if !bytes.Equal(pretty.Bytes(), want) {
		t.Errorf("request differs from %s", path)
	}
}
