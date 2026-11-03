package serve

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/store"
)

// dashboardStore holds three targets: a ok and probed, b down with an open
// incident, and c never probed; plus a resolved and an administratively
// closed incident, and one too old to show.
func dashboardStore(t *testing.T) (*store.DB, time.Time) {
	t.Helper()
	db := openStore(t)
	now := t0.Add(10*time.Minute + 12*time.Second)
	insert(t, db, healthy("a", t0.Add(5*time.Minute)), healthy("a", t0.Add(10*time.Minute)))
	rejected := []store.Sample{turn("b", t0.Add(9*time.Minute), 1)}
	rejected[0].HTTPStatus, rejected[0].Error, rejected[0].TTFT = 401, "http 401: invalid x-api-key", 0
	insert(t, db, rejected)

	incident := func(target, state, cause, detail string, opened, closed time.Time, resolved bool) {
		t.Helper()
		inc := store.Incident{Target: target, Scenario: "standard-v1", State: state, Cause: cause,
			Detail: detail, Opened: opened}
		id, err := db.OpenIncident(t.Context(), inc, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !closed.IsZero() {
			inc.ID, inc.Closed, inc.Resolved, inc.CloseCause = id, closed, resolved, "back to ok for 3 probes in a row"
			if !resolved {
				inc.CloseCause = "target removed from config"
			}
			if err := db.CloseIncident(t.Context(), inc, nil, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	incident("a", "slow", "slower than usual for this hour", "TTFT 2400 ms, usually 900 ms", t0.Add(-8*24*time.Hour), t0.Add(-8*24*time.Hour+time.Hour), true)
	incident("a", "errors", "the provider is overloaded", "turn 2: http 529: overloaded_error: Overloaded", t0.Add(-26*time.Hour), t0.Add(-26*time.Hour+14*time.Minute), true)
	incident("old", "down", "the key was rejected", "", t0.Add(-3*time.Hour), t0.Add(-2*time.Hour), false)
	incident("b", "down", "the key was rejected: check its value, its permissions and the account's billing",
		"turn 1: http 401: invalid x-api-key", t0.Add(-12*time.Minute), time.Time{}, false)

	for _, ts := range []store.TargetState{
		{Target: "a", Scenario: "standard-v1", State: "ok", Since: t0.Add(-26*time.Hour + 14*time.Minute), LastState: "ok", Last: t0.Add(10 * time.Minute)},
		{Target: "b", Scenario: "standard-v1", State: "down", Since: t0.Add(-14 * time.Minute),
			Cause:     "the key was rejected: check its value, its permissions and the account's billing",
			LastState: "down", LastDetail: "turn 1: http 401: invalid x-api-key", Last: t0.Add(9 * time.Minute), IncidentID: 4},
	} {
		if err := db.SaveTargetState(t.Context(), ts); err != nil {
			t.Fatal(err)
		}
	}
	return db, now
}

func dashboardHandler(t *testing.T, db store.Store, now *time.Time) http.Handler {
	t.Helper()
	opts := options(now)
	opts.Targets = append(opts.Targets, config.Target{Name: "c", Price: &config.DefaultPrice})
	h, err := Handler(t.Context(), db, opts)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestDashboardGolden(t *testing.T) {
	db, now := dashboardStore(t)
	h := dashboardHandler(t, db, &now)
	code, got := get(t, h, "/")
	if code != http.StatusOK {
		t.Fatalf("got %d: %s", code, got)
	}
	// The version changes with every asset edit; the golden checks where it goes.
	got = strings.ReplaceAll(got, assetVersion, "VERSION")
	path := filepath.Join("testdata", "dashboard.golden.html")
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
		t.Errorf("dashboard differs from %s (run with -update to accept):\n%s", path, got)
	}
}

func TestLiveIsTheFragmentThePageEmbeds(t *testing.T) {
	db, now := dashboardStore(t)
	h := dashboardHandler(t, db, &now)
	_, page := get(t, h, "/")
	_, live := get(t, h, "/live")
	if strings.Contains(live, "<html") || !strings.HasPrefix(live, `<main id="live" hx-get="/live" hx-trigger="every 10s"`) {
		t.Fatalf("/live is not the fragment:\n%s", live)
	}
	if !strings.Contains(page, live) {
		t.Error("the page does not embed the same fragment /live serves")
	}
}

func TestBannerIsCalmWhenEveryTargetIsOK(t *testing.T) {
	db := openStore(t)
	now := t0
	if err := db.SaveTargetState(t.Context(), store.TargetState{Target: "a", Scenario: "standard-v1", State: "ok", Since: t0, Last: t0}); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, handler(t, db, &now), "/live")
	if !strings.Contains(body, "All targets operating normally") || strings.Contains(body, " is ok") {
		t.Errorf("want the calm banner:\n%s", body)
	}
}

func TestBannerWaitsForTheFirstProbe(t *testing.T) {
	db := openStore(t)
	now := t0
	_, body := get(t, handler(t, db, &now), "/live")
	if !strings.Contains(body, "Waiting for the first probe") || strings.Contains(body, "operating normally") {
		t.Errorf("want the waiting banner before any probe:\n%s", body)
	}
}

func TestBannerListsProblemsMostSevereFirst(t *testing.T) {
	db := openStore(t)
	now := t0
	for _, ts := range []store.TargetState{
		{Target: "a", Scenario: "standard-v1", State: "slow", Since: t0, Last: t0},
		{Target: "b", Scenario: "standard-v1", State: "gone", Since: t0, Last: t0},
	} {
		if err := db.SaveTargetState(t.Context(), ts); err != nil {
			t.Fatal(err)
		}
	}
	_, body := get(t, handler(t, db, &now), "/live")
	gone, slow := strings.Index(body, "b is gone"), strings.Index(body, "a is slow")
	if gone < 0 || slow < 0 || gone > slow {
		t.Errorf("want b is gone before a is slow:\n%s", body)
	}
}

func TestDashboardEscapesWhatProvidersSay(t *testing.T) {
	db := openStore(t)
	now := t0
	err := db.SaveTargetState(t.Context(), store.TargetState{Target: "a", Scenario: "standard-v1", State: "errors",
		Since: t0, Cause: `<script>alert(1)</script>`, Last: t0})
	if err != nil {
		t.Fatal(err)
	}
	_, body := get(t, handler(t, db, &now), "/live")
	if strings.Contains(body, "<script>alert") || !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("the cause was not escaped:\n%s", body)
	}
}

func TestAssetsAreEmbeddedAndTyped(t *testing.T) {
	db := openStore(t)
	now := t0
	h := handler(t, db, &now)
	for path, typ := range map[string]string{
		"piplon.css":                             "text/css",
		"htmx.min.js":                            "javascript",
		"mark.svg":                               "image/svg+xml",
		"fonts/IBMPlexSans-Regular-Latin1.woff2": "font/woff2",
		"fonts/IBMPlexMono-Medium-Latin1.woff2":  "font/woff2",
	} {
		rec := getRecorder(t, h, "/assets/"+path+"?v="+assetVersion)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), typ) {
			t.Errorf("%s: got %d %q, want 200 %s", path, rec.Code, rec.Header().Get("Content-Type"), typ)
		}
	}
	for _, path := range []string{"/assets/", "/assets/fonts/", "/assets/nope.js", "/templates/page.html", "/nope"} {
		if code, _ := get(t, h, path); code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", path, code)
		}
	}
}

func TestAssetURLsChangeWithTheirContent(t *testing.T) {
	db := openStore(t)
	now := t0
	h := handler(t, db, &now)
	v := "?v=" + assetVersion
	_, page := get(t, h, "/")
	for _, ref := range []string{`href="/assets/piplon.css` + v + `"`, `src="/assets/htmx.min.js` + v + `"`, `href="/assets/mark.svg` + v + `"`} {
		if !strings.Contains(page, ref) {
			t.Errorf("the page does not reference %s", ref)
		}
	}
	// The stylesheet's own references carry the version too.
	_, css := get(t, h, "/assets/piplon.css"+v)
	for _, ref := range []string{`url("fonts/IBMPlexSans-Regular-Latin1.woff2` + v + `")`, `url("mark.svg` + v + `")`} {
		if !strings.Contains(css, ref) {
			t.Errorf("the stylesheet does not reference %s", ref)
		}
	}
	if strings.Contains(strings.ReplaceAll(css, v+`")`, `")`), v) {
		t.Error("a url() got the version twice")
	}
	if rec := getRecorder(t, h, "/assets/piplon.css"+v); rec.Header().Get("Cache-Control") != "public, max-age=86400" {
		t.Errorf("current version: Cache-Control %q, want public, max-age=86400", rec.Header().Get("Cache-Control"))
	}
	// Another version or none still gets the file, but no browser keeps it.
	for _, path := range []string{"/assets/piplon.css?v=0123456789ab", "/assets/piplon.css"} {
		if rec := getRecorder(t, h, path); rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: got %d %q, want 200 no-cache", path, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
	a := fstest.MapFS{"piplon.css": {Data: []byte("a{}")}, "fonts/x.woff2": {Data: []byte("x")}}
	b := fstest.MapFS{"piplon.css": {Data: []byte("a{ }")}, "fonts/x.woff2": {Data: []byte("x")}}
	same := fstest.MapFS{"fonts/x.woff2": {Data: []byte("x")}, "piplon.css": {Data: []byte("a{}")}}
	if va, vb := versionOf(a), versionOf(b); va == vb || versionOf(same) != va || len(va) != 12 {
		t.Errorf("versionOf: %s for a and for the same files, %s for b; want 12 digits that change with content", va, vb)
	}
}

func TestIncidentNumberAndDateAreApartWithoutCSS(t *testing.T) {
	db, now := dashboardStore(t)
	_, live := get(t, dashboardHandler(t, db, &now), "/live")
	if !strings.Contains(live, `<span class="ref">#4</span> 2026-11-03 08:48 UTC`) {
		t.Errorf("want a space between the number and the date:\n%s", live)
	}
}

func TestEveryResponseKeepsToItsOwnOrigin(t *testing.T) {
	db := openStore(t)
	now := t0
	h := handler(t, db, &now)
	for _, path := range []string{"/", "/live", "/metrics", "/assets/piplon.css?v=" + assetVersion} {
		rec := getRecorder(t, h, path)
		if csp := rec.Header().Get("Content-Security-Policy"); csp != "default-src 'self'; frame-ancestors 'none'" {
			t.Errorf("%s: Content-Security-Policy %q", path, csp)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: no nosniff", path)
		}
	}
	_, page := get(t, h, "/")
	for _, external := range []string{"http://", "https://", "//cdn", "<style", "style="} {
		if strings.Contains(page, external) {
			t.Errorf("the page contains %q", external)
		}
	}
}

func TestDashboardOnAnUnreadableStore(t *testing.T) {
	db := openStore(t)
	now := t0
	h := handler(t, db, &now)
	_ = db.Close()
	if code, _ := get(t, h, "/live"); code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503 so htmx keeps the last view", code)
	}
}
