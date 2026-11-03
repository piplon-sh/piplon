package notify

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/store"
)

func TestDiscordMessages(t *testing.T) {
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
		{"discord_opened.golden.json", store.Notification{Event: store.EventOpened, Incident: incident()}},
		{"discord_updated_slow.golden.json", store.Notification{Event: store.EventUpdated, Incident: slow}},
		{"discord_resolved.golden.json", store.Notification{Event: store.EventClosed, Incident: resolved}},
		{"discord_closed_without_resolution.golden.json", store.Notification{Event: store.EventClosed, Incident: removed}},
	} {
		t.Run(tt.file, func(t *testing.T) {
			srv, body := capture(t, http.StatusNoContent)
			if err := (Discord{URL: config.Secret(srv.URL)}).Send(t.Context(), tt.n); err != nil {
				t.Fatal(err)
			}
			checkGolden(t, tt.file, *body)
		})
	}
}

func TestDiscordEscapesAndNeverMentions(t *testing.T) {
	inc := incident()
	inc.Target = "my_target*"
	inc.Cause = "@everyone the provider said ~so~ |loud|"
	inc.Detail = "turn 1: `bad` input"
	doc := discordMessage(store.Notification{Event: store.EventOpened, Incident: inc})
	e := doc.Embeds[0]
	if e.Title != `🔴 my\_target\* is down` {
		t.Errorf("title: %q", e.Title)
	}
	if got := e.Fields[0].Value; got != `@everyone the provider said \~so\~ \|loud\|` {
		t.Errorf("cause: %q", got)
	}
	if got := e.Fields[1].Value; got != "`turn 1: 'bad' input`" {
		t.Errorf("measured: %q", got)
	}
	raw, _ := json.Marshal(doc)
	if !strings.Contains(string(raw), `"allowed_mentions":{"parse":[]}`) {
		t.Errorf("mentions are not disabled: %s", raw)
	}
}

func TestDiscordColoursAndLimits(t *testing.T) {
	for state, want := range map[string]int{"down": 0xD9480F, "slow": 0xD98B0F} {
		inc := incident()
		inc.State = state
		if got := discordMessage(store.Notification{Event: store.EventOpened, Incident: inc}).Embeds[0].Color; got != want {
			t.Errorf("%s: colour %#x, want %#x", state, got, want)
		}
	}
	inc := incident()
	inc.Target = strings.Repeat("t", 300)
	inc.Detail = strings.Repeat("d", 2000)
	e := discordMessage(store.Notification{Event: store.EventOpened, Incident: inc}).Embeds[0]
	if n := len([]rune(e.Title)); n != discordTitleMax {
		t.Errorf("title is %d characters, want %d", n, discordTitleMax)
	}
	if n := len([]rune(e.Fields[1].Value)); n != discordFieldMax || !strings.HasSuffix(e.Fields[1].Value, "…`") {
		t.Errorf("measured is %d characters, want %d and still closed as code", n, discordFieldMax)
	}
}

func TestDiscordErrorsHideTheURL(t *testing.T) {
	srv, _ := capture(t, http.StatusTooManyRequests)
	n := store.Notification{Event: store.EventOpened, Incident: incident()}
	if err := (Discord{URL: config.Secret(srv.URL + "/api/webhooks/1/secret-token")}).Send(t.Context(), n); err == nil || err.Error() != "http 429" {
		t.Errorf("non-2xx: got %v, want http 429", err)
	}
	err := (Discord{URL: "http://127.0.0.1:1/api/webhooks/1/secret-token"}).Send(t.Context(), n)
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Errorf("transport error: got %v, want one without the URL", err)
	}
}
