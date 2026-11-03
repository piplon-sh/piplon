package serve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/piplon-sh/piplon/internal/analyze"
	"github.com/piplon-sh/piplon/internal/store"
)

// The dashboard reads the last hour of samples for the targets table and the
// last 7 days of incidents.
const (
	recentWindow   = time.Hour
	incidentWindow = 7 * 24 * time.Hour
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed assets
var assetFS embed.FS

// assetVersion names the embedded assets' content. Every asset URL carries
// it, /assets/piplon.css?v=<version>, the stylesheet's own url() references
// included, so a piplon with other assets never gets an old copy from a
// browser cache.
var assetVersion = versionOf(assetFS)

var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"asset": func(path string) string { return "/assets/" + path + "?v=" + assetVersion },
}).ParseFS(templateFS, "templates/*.html"))

// stylesheet is piplon.css with ?v= added to each url(), served instead of
// the embedded file.
var stylesheet = versionURLs(mustRead(assetFS, "assets/piplon.css"), assetVersion)

var cssURL = regexp.MustCompile(`url\("([^"?]+)"\)`)

func versionURLs(css []byte, version string) []byte {
	return cssURL.ReplaceAll(css, []byte(`url("$1?v=`+version+`")`))
}

func mustRead(fsys fs.FS, path string) []byte {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		panic(err) // embedded at build time: cannot fail at run time
	}
	return data
}

// versionOf hashes every file's path and content, in walk order, which is
// lexical: 12 hex digits.
func versionOf(fsys fs.FS) string {
	h := sha256.New()
	err := fs.WalkDir(fsys, ".", func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", path, len(data))
		_, _ = h.Write(data)
		return nil
	})
	if err != nil {
		panic(err) // embedded at build time: cannot fail at run time
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// dashboard serves / and the /live fragment htmx swaps in every 10 s.
type dashboard struct {
	db   store.Store
	m    *metrics
	mu   *sync.Mutex // shared with /metrics: both refresh m
	opts Options
}

func addDashboard(mux *http.ServeMux, db store.Store, m *metrics, mu *sync.Mutex, opts Options) error {
	assets, err := fs.Sub(assetFS, "assets")
	if err != nil {
		return err
	}
	d := &dashboard{db: db, m: m, mu: mu, opts: opts}
	mux.HandleFunc("GET /{$}", d.render("page"))
	mux.HandleFunc("GET /live", d.render("live"))
	addAPI(mux, d)
	files := http.StripPrefix("/assets/", http.FileServerFS(assets))
	mux.HandleFunc("GET /assets/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r) // no directory listings
			return
		}
		// Only the current version is cached: a page from another piplon,
		// or a URL without ?v=, still gets today's file, never kept.
		if r.URL.Query().Get("v") == assetVersion {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if r.URL.Path == "/assets/piplon.css" {
			http.ServeContent(w, r, "piplon.css", time.Time{}, bytes.NewReader(stylesheet))
			return
		}
		files.ServeHTTP(w, r)
	})
	return nil
}

// render writes the named template with the current view. A store it cannot
// read is a 503, which htmx does not swap in, so the page keeps the last
// view it had.
func (d *dashboard) render(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snap, err := d.snapshot(r.Context(), func(now time.Time) time.Time { return now.Add(-incidentWindow) })
		if err != nil {
			d.opts.Log.Error("dashboard not read from the store", "err", err)
			http.Error(w, "store not readable", http.StatusServiceUnavailable)
			return
		}
		var buf bytes.Buffer
		if err := templates.ExecuteTemplate(&buf, name, viewOf(snap)); err != nil {
			d.opts.Log.Error("dashboard not rendered", "err", err)
			http.Error(w, "dashboard not rendered", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = buf.WriteTo(w)
	}
}

// snapshot reads what the dashboard and the API show, once, with the
// incidents open at sinceOf(now) or after. The metrics refresh gives the
// target states and the day's spend, the same figure as
// piplon_budget_spent_usd.
func (d *dashboard) snapshot(ctx context.Context, sinceOf func(now time.Time) time.Time) (snapshot, error) {
	d.mu.Lock()
	err := d.m.refresh(ctx)
	states, spent, now := slices.Clone(d.m.states), d.m.spent, d.m.now
	d.mu.Unlock()
	if err != nil {
		return snapshot{}, err
	}
	snap := snapshot{Now: now, Since: sinceOf(now), DailyUSD: d.opts.DailyUSD, SpentUSD: spent}
	samples, err := d.db.Samples(ctx, store.Query{Since: now.Add(-recentWindow)})
	if err != nil {
		return snapshot{}, err
	}
	if snap.Incidents, err = d.db.Incidents(ctx, snap.Since); err != nil {
		return snapshot{}, err
	}
	byTarget := map[string][]store.Sample{}
	for _, s := range samples {
		byTarget[s.Target] = append(byTarget[s.Target], s)
	}
	stateOf := map[string]store.TargetState{}
	for _, s := range states {
		stateOf[s.Target] = s
	}
	for _, t := range d.opts.Targets {
		ts := targetSnap{Name: t.Name, Format: t.Format, Model: t.Model,
			Recent: analyze.Summarize(byTarget[t.Name], d.opts.CacheFrom)}
		if state, probed := stateOf[t.Name]; probed {
			ts.State = &state
		}
		snap.Targets = append(snap.Targets, ts)
	}
	return snap, nil
}
