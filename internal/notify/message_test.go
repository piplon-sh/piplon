package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/store"
)

var opened = time.Date(2026, 11, 3, 9, 2, 0, 0, time.UTC)

func incident() store.Incident {
	return store.Incident{ID: 12, Target: "anthropic-direct", Scenario: "standard-v1", State: "down",
		Cause:  "the key was rejected: check its value, its permissions and the account's billing",
		Detail: "turn 1: http 401: authentication_error: invalid x-api-key", Opened: opened, Updated: opened}
}

func TestText(t *testing.T) {
	updated := incident()
	updated.State, updated.Cause, updated.Detail = "errors", "the provider is overloaded", "turn 2: http 529: overloaded_error: Overloaded"
	resolved := updated
	resolved.Closed, resolved.CloseCause, resolved.Resolved = opened.Add(14*time.Minute+20*time.Second), "back to ok for 3 probes in a row", true
	removed := incident()
	removed.Closed, removed.CloseCause = opened.Add(26*time.Hour), "target removed from config"
	noDetail := incident()
	noDetail.Detail = ""

	for _, tt := range []struct {
		name string
		n    store.Notification
		want string
	}{
		{"opened", store.Notification{Event: store.EventOpened, Incident: incident()}, `piplon · anthropic-direct is down
Probable cause: the key was rejected: check its value, its permissions and the account's billing
Measured: turn 1: http 401: authentication_error: invalid x-api-key
Incident #12 · standard-v1 · opened 2026-11-03 09:02 UTC`},
		{"updated", store.Notification{Event: store.EventUpdated, Incident: updated}, `piplon · anthropic-direct is now errors
Probable cause: the provider is overloaded
Measured: turn 2: http 529: overloaded_error: Overloaded
Incident #12 · standard-v1 · open since 2026-11-03 09:02 UTC`},
		{"resolved", store.Notification{Event: store.EventClosed, Incident: resolved}, `piplon · anthropic-direct is back to ok
Incident #12 · standard-v1 · resolved after 14 min · was errors`},
		{"administrative close", store.Notification{Event: store.EventClosed, Incident: removed}, `piplon · incident #12 on anthropic-direct closed without resolution
Reason: target removed from config · was down since 2026-11-03 09:02 UTC`},
		{"no detail", store.Notification{Event: store.EventOpened, Incident: noDetail}, `piplon · anthropic-direct is down
Probable cause: the key was rejected: check its value, its permissions and the account's billing
Incident #12 · standard-v1 · opened 2026-11-03 09:02 UTC`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Text(tt.n); got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
	if got := Text(store.Notification{Event: store.EventClosed, Incident: removed}); strings.Contains(got, "resolved") || strings.Contains(got, "back to ok") {
		t.Errorf("an administrative close reads as a resolution: %q", got)
	}
}

func TestSpan(t *testing.T) {
	for d, want := range map[time.Duration]string{40 * time.Second: "40 s", 14 * time.Minute: "14 min", 2*time.Hour + 5*time.Minute: "2 h 05 min"} {
		if got := span(d); got != want {
			t.Errorf("%v: got %q, want %q", d, got, want)
		}
	}
}

func TestLastedMatchesTheRoundedTimes(t *testing.T) {
	start := time.Date(2026, 11, 3, 12, 20, 10, 0, time.UTC)
	for _, tt := range []struct {
		open        time.Duration
		lasted, per string
	}{
		{3*time.Minute + 50*time.Second, "4 min", "2026-11-03 12:20 → 12:24 UTC"}, // 12:20:10 → 12:24:00
		{3*time.Minute + 15*time.Second, "3 min", "2026-11-03 12:20 → 12:23 UTC"}, // 12:20:10 → 12:23:25
		{40 * time.Second, "1 min", "2026-11-03 12:20 → 12:21 UTC"},               // 12:20:10 → 12:20:50
		{10 * time.Second, "10 s", "2026-11-03 12:20 → 12:20 UTC"},
	} {
		inc := store.Incident{Opened: start, Closed: start.Add(tt.open)}
		if got, per := lasted(inc), period(inc); got != tt.lasted || per != tt.per {
			t.Errorf("open %v: got %q %q, want %q %q", tt.open, got, per, tt.lasted, tt.per)
		}
	}
}
