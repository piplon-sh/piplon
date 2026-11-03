package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/store"
)

// secretKey and secretURL are set on every target of apiHandler: no response
// may contain them.
const (
	secretKey = "sk-ant-api03-secret-value-for-tests" //nolint:gosec // a fake key the test looks for
	secretURL = "https://gateway.internal.example/v1" //nolint:gosec // a fake URL the test looks for
)

func apiHandler(t *testing.T, db store.Store, now *time.Time) http.Handler {
	t.Helper()
	opts := options(now)
	opts.Targets = append(opts.Targets, config.Target{Name: "c", Price: &config.DefaultPrice})
	formats := map[string][2]string{"a": {"anthropic", "claude-sonnet-4-5"}, "b": {"openai", "gpt-4.1"}, "c": {"ollama", "llama3.2"}}
	for i := range opts.Targets {
		f := formats[opts.Targets[i].Name]
		opts.Targets[i].Format, opts.Targets[i].Model = f[0], f[1]
		opts.Targets[i].Key, opts.Targets[i].URL = config.Secret(secretKey), secretURL
	}
	h, err := Handler(t.Context(), db, opts)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
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
}

func TestAPIGolden(t *testing.T) {
	db, now := dashboardStore(t)
	h := apiHandler(t, db, &now)
	for path, file := range map[string]string{
		"/api/v1/targets":              "api_targets.golden.json",
		"/api/v1/incidents":            "api_incidents.golden.json",
		"/api/v1/incidents?state=open": "api_incidents_open.golden.json",
	} {
		rec := getRecorder(t, h, path)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%s: got %d %q", path, rec.Code, rec.Header().Get("Content-Type"))
		}
		golden(t, file, rec.Body.String())
	}
}

func TestAPIIsTheDashboardsView(t *testing.T) {
	db, now := dashboardStore(t)
	h := apiHandler(t, db, &now)
	var targets targetsResponse
	var incidents incidentsResponse
	decode(t, h, "/api/v1/targets", &targets)
	decode(t, h, "/api/v1/incidents", &incidents)
	_, live := get(t, h, "/live")
	// The same three targets in config order, the same three incidents
	// newest first, and the same spend.
	var names []string
	for _, tgt := range targets.Targets {
		names = append(names, tgt.Name)
	}
	if strings.Join(names, ",") != "a,b,c" || len(incidents.Incidents) != 3 || incidents.Incidents[0].ID != 4 {
		t.Fatalf("targets %v, incidents %+v", names, incidents.Incidents)
	}
	if spent := fmt.Sprintf("%.2f of 5.00 USD today", targets.Budget.SpentTodayUSD); !strings.Contains(live, spent) {
		t.Errorf("the API spends %q, not what the dashboard shows", spent)
	}
}

func decode(t *testing.T, h http.Handler, path string, v any) {
	t.Helper()
	code, body := get(t, h, path)
	if code != http.StatusOK {
		t.Fatalf("%s: got %d: %s", path, code, body)
	}
	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func TestAPINeverShowsKeysOrURLs(t *testing.T) {
	db, now := dashboardStore(t)
	h := apiHandler(t, db, &now)
	// The handler is given targets only: webhook URLs never reach it.
	for _, path := range []string{"/api/v1/targets", "/api/v1/incidents", "/", "/live", "/metrics"} {
		_, body := get(t, h, path)
		for _, secret := range []string{secretKey, "secret-value", secretURL, "gateway.internal"} {
			if strings.Contains(body, secret) {
				t.Errorf("%s contains %q", path, secret)
			}
		}
	}
}

func TestIncidentsSince(t *testing.T) {
	db, now := dashboardStore(t)
	h := apiHandler(t, db, &now)
	for _, tt := range []struct {
		since string
		want  string // incident IDs, newest first
		from  string
	}{
		{"", "4,3,2", "2026-10-27T09:10:12Z"},
		{"7d", "4,3,2", "2026-10-27T09:10:12Z"},
		{"9d", "4,3,2,1", "2026-10-25T09:10:12Z"},
		{"4h", "4,3", "2026-11-03T05:10:12Z"},
		{"2h", "4", "2026-11-03T07:10:12Z"}, // #3 closed at 07:00
		{"90m", "4", "2026-11-03T07:40:12Z"},
		{"2026-11-03T06:30:00Z", "4,3", "2026-11-03T06:30:00Z"},
	} {
		var got incidentsResponse
		decode(t, h, "/api/v1/incidents?since="+tt.since, &got)
		var ids []string
		for _, inc := range got.Incidents {
			ids = append(ids, strconv.FormatInt(inc.ID, 10))
		}
		if strings.Join(ids, ",") != tt.want || got.Since != tt.from {
			t.Errorf("since=%s: got %v from %s, want %s from %s", tt.since, ids, got.Since, tt.want, tt.from)
		}
	}
}

func TestAPIRejectsBadParameters(t *testing.T) {
	db, now := dashboardStore(t)
	h := apiHandler(t, db, &now)
	for _, path := range []string{
		"/api/v1/incidents?since=yesterday", "/api/v1/incidents?since=-2h", "/api/v1/incidents?since=0d",
		"/api/v1/incidents?since=2026-11-03", "/api/v1/incidents?state=closed",
	} {
		rec := getRecorder(t, h, path)
		var body map[string]string
		if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["error"] == "" {
			t.Errorf("%s: got %d %s, want 400 with an error", path, rec.Code, rec.Body)
		}
	}
}

func TestAPIOnAnUnreadableStore(t *testing.T) {
	db := openStore(t)
	now := t0
	h := handler(t, db, &now)
	_ = db.Close()
	for _, path := range []string{"/api/v1/targets", "/api/v1/incidents"} {
		if rec := getRecorder(t, h, path); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error"`) {
			t.Errorf("%s: got %d %s, want 503 with an error", path, rec.Code, rec.Body)
		}
	}
}

func TestAPIBeforeAnyProbeHasEmptyListsNotNull(t *testing.T) {
	db := openStore(t)
	now := t0
	h := handler(t, db, &now)
	_, incidents := get(t, h, "/api/v1/incidents")
	if !strings.Contains(incidents, `"incidents": []`) {
		t.Errorf("want an empty list:\n%s", incidents)
	}
	_, targets := get(t, h, "/api/v1/targets")
	if !strings.Contains(targets, `"state": null`) || !strings.Contains(targets, `"last_probe": null`) {
		t.Errorf("want null fields for targets never probed:\n%s", targets)
	}
}
