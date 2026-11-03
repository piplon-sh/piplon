package notify

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/store"
)

// capture serves status and keeps the last request body.
func capture(t *testing.T, status int) (*httptest.Server, *[]byte) {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type %q", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

// checkGolden compares an indented JSON body with testdata/<file>.
func checkGolden(t *testing.T, file string, body []byte) {
	t.Helper()
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	path := filepath.Join("testdata", file)
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
		t.Errorf("message differs from %s:\n%s", path, pretty.Bytes())
	}
}

func TestSlackMessages(t *testing.T) {
	slow := incident()
	slow.State, slow.Cause, slow.Detail = "slow", "slower than usual at this time: the provider or the path to it is degraded", "TTFT 2400 ms, usually 1100 ms"
	resolved := incident() // down, the key was rejected: check …
	// 09:02:00 → 09:05:40: shown as 09:02 → 09:06, so 4 min, not 3.
	resolved.Closed, resolved.CloseCause, resolved.Resolved = opened.Add(3*time.Minute+40*time.Second), "back to ok for 3 probes in a row", true
	removed := incident()
	removed.Closed, removed.CloseCause = opened.Add(26*time.Hour), "target removed from config"
	for _, tt := range []struct {
		file string
		n    store.Notification
	}{
		{"slack_opened.golden.json", store.Notification{Event: store.EventOpened, Incident: incident()}},
		{"slack_updated_slow.golden.json", store.Notification{Event: store.EventUpdated, Incident: slow}},
		{"slack_resolved.golden.json", store.Notification{Event: store.EventClosed, Incident: resolved}},
		{"slack_closed_without_resolution.golden.json", store.Notification{Event: store.EventClosed, Incident: removed}},
	} {
		t.Run(tt.file, func(t *testing.T) {
			srv, body := capture(t, http.StatusOK)
			if err := (Slack{URL: config.Secret(srv.URL)}).Send(t.Context(), tt.n); err != nil {
				t.Fatal(err)
			}
			checkGolden(t, tt.file, *body)
		})
	}
}

func TestSlackEscapesMarkup(t *testing.T) {
	inc := incident()
	inc.Target = "a<b>&c"
	inc.Detail = "max_tokens must be < 4096 & `int`"
	doc := slackMessage(store.Notification{Event: store.EventOpened, Incident: inc})
	blocks := doc.Attachments[0].Blocks
	if title := blocks[0].Text.Text; title != "*🔴 a&lt;b&gt;&amp;c is down*" {
		t.Errorf("title: %q", title)
	}
	if measured := blocks[2].Text.Text; measured != "*Measured*\n`max_tokens must be &lt; 4096 &amp; 'int'`" {
		t.Errorf("measured: %q", measured)
	}
	if fallback := doc.Attachments[0].Fallback; !strings.HasPrefix(fallback, "piplon · a&lt;b&gt;&amp;c is down") {
		t.Errorf("fallback: %q", fallback)
	}
}

func TestSlackColoursAndEmoji(t *testing.T) {
	for state, want := range map[string]struct{ colour, emoji string }{
		"down": {colourFailure, "🔴"}, "errors": {colourFailure, "🔴"}, "gone": {colourFailure, "🔴"},
		"slow": {colourWarning, "🟠"}, "cache_miss": {colourWarning, "🟠"}, "refused": {colourWarning, "🟠"},
	} {
		inc := incident()
		inc.State = state
		a := slackMessage(store.Notification{Event: store.EventOpened, Incident: inc}).Attachments[0]
		if a.Color != want.colour || !strings.HasPrefix(a.Blocks[0].Text.Text, "*"+want.emoji+" ") {
			t.Errorf("%s: got %s %q, want %s and %s", state, a.Color, a.Blocks[0].Text.Text, want.colour, want.emoji)
		}
	}
}

func TestWebhookDocument(t *testing.T) {
	srv, body := capture(t, http.StatusAccepted)
	open := store.Notification{ID: 7, Event: store.EventOpened, At: opened, Incident: incident()}
	if err := (Webhook{URL: config.Secret(srv.URL)}).Send(t.Context(), open); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(*body, &doc); err != nil {
		t.Fatal(err)
	}
	inc := doc["incident"].(map[string]any)
	if doc["version"] != "piplon.incident.v1" || doc["id"] != 7.0 || doc["event"] != "opened" || doc["at"] != "2026-11-03T09:02:00Z" ||
		inc["state"] != "down" || inc["closed"] != nil || inc["resolved"] != false || inc["target"] != "anthropic-direct" {
		t.Errorf("got %s", *body)
	}

	removed := incident()
	removed.Closed, removed.CloseCause = opened.Add(26*time.Hour), "target removed from config"
	if err := (Webhook{URL: config.Secret(srv.URL)}).Send(t.Context(), store.Notification{ID: 8, Event: store.EventClosed, At: removed.Closed, Incident: removed}); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(*body, &doc)
	inc = doc["incident"].(map[string]any)
	if inc["closed"] != "2026-11-04T11:02:00Z" || inc["resolved"] != false || inc["close_cause"] != "target removed from config" {
		t.Errorf("administrative close: got %s", *body)
	}
}

func TestPostErrorsHideTheURL(t *testing.T) {
	srv, _ := capture(t, http.StatusServiceUnavailable)
	n := store.Notification{Event: store.EventOpened, Incident: incident()}
	if err := (Slack{URL: config.Secret(srv.URL + "/services/T0/B0/secret-token")}).Send(t.Context(), n); err == nil || err.Error() != "http 503" {
		t.Errorf("non-2xx: got %v, want http 503", err)
	}
	// Port 1 on loopback refuses at once.
	err := (Webhook{URL: "http://127.0.0.1:1/hooks/secret-token"}).Send(t.Context(), n)
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Errorf("transport error: got %v, want one without the URL", err)
	}
}
