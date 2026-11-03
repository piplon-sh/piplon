package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// apiVersion is the JSON contract of /api/v1: fields may be added, never
// renamed, removed or retyped. Every field is always present, null when it
// has no value.
const apiVersion = "v1"

type targetsResponse struct {
	APIVersion  string      `json:"api_version"`
	GeneratedAt string      `json:"generated_at"`
	Budget      budgetJSON  `json:"budget"`
	Targets     []targetAPI `json:"targets"`
}

type budgetJSON struct {
	DailyUSD      float64 `json:"daily_usd"`
	SpentTodayUSD float64 `json:"spent_today_usd"`
}

type targetAPI struct {
	Name       string         `json:"name"`
	Format     string         `json:"format"`
	Model      string         `json:"model"`
	Scenario   *string        `json:"scenario"`
	State      *string        `json:"state"`
	Since      *string        `json:"since"`
	Cause      *string        `json:"cause"`
	IncidentID *int64         `json:"incident_id"`
	LastProbe  *lastProbeJSON `json:"last_probe"`
	LastHour   lastHourJSON   `json:"last_hour"`
}

type lastProbeJSON struct {
	At     string  `json:"at"`
	State  string  `json:"state"`
	Detail *string `json:"detail"` // null when ok
}

type lastHourJSON struct {
	Probes         int      `json:"probes"`
	ErrorRatio     *float64 `json:"error_ratio"`
	TTFTP50Seconds *float64 `json:"ttft_p50_seconds"`
	Cache          *string  `json:"cache"`
}

type incidentsResponse struct {
	APIVersion  string        `json:"api_version"`
	GeneratedAt string        `json:"generated_at"`
	Since       string        `json:"since"`
	Incidents   []incidentAPI `json:"incidents"`
}

type incidentAPI struct {
	ID              int64   `json:"id"`
	Target          string  `json:"target"`
	Scenario        string  `json:"scenario"`
	Status          string  `json:"status"` // open, resolved, or closed for an administrative close
	State           string  `json:"state"`
	Cause           string  `json:"cause"`
	Detail          *string `json:"detail"`
	OpenedAt        string  `json:"opened_at"`
	UpdatedAt       string  `json:"updated_at"`
	ClosedAt        *string `json:"closed_at"`
	CloseCause      *string `json:"close_cause"`
	DurationSeconds int64   `json:"duration_seconds"`
}

func addAPI(mux *http.ServeMux, d *dashboard) {
	mux.HandleFunc("GET /api/v1/targets", func(w http.ResponseWriter, r *http.Request) {
		snap, err := d.snapshot(r.Context(), func(now time.Time) time.Time { return now })
		if err != nil {
			d.unreadable(w, err)
			return
		}
		writeJSON(w, http.StatusOK, targetsOf(snap))
	})
	mux.HandleFunc("GET /api/v1/incidents", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		sinceOf, err := parseSince(q.Get("since"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		openOnly := false
		switch state := q.Get("state"); state {
		case "":
		case "open":
			openOnly = true
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("state %q: only open is supported", state)})
			return
		}
		snap, err := d.snapshot(r.Context(), sinceOf)
		if err != nil {
			d.unreadable(w, err)
			return
		}
		writeJSON(w, http.StatusOK, incidentsOf(snap, openOnly))
	})
}

func (d *dashboard) unreadable(w http.ResponseWriter, err error) {
	d.opts.Log.Error("api not read from the store", "err", err)
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not readable"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		http.Error(w, `{"error": "response not encoded"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(append(data, '\n'))
}

// parseSince reads ?since=: an RFC 3339 time, or a positive duration back
// from now such as 90m, 24h or 7d. Empty is 7 days.
func parseSince(s string) (func(now time.Time) time.Time, error) {
	if s == "" {
		return func(now time.Time) time.Time { return now.Add(-incidentWindow) }, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return func(time.Time) time.Time { return t }, nil
	}
	d, err := time.ParseDuration(s)
	if days, ok := strings.CutSuffix(s, "d"); ok {
		var n int
		n, err = strconv.Atoi(days)
		d = time.Duration(n) * 24 * time.Hour
	}
	if err != nil || d <= 0 {
		return nil, errors.New("since: want an RFC 3339 time or a duration such as 24h or 7d")
	}
	return func(now time.Time) time.Time { return now.Add(-d) }, nil
}

// rfc3339 is a time in UTC to the second.
func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func targetsOf(snap snapshot) targetsResponse {
	out := targetsResponse{APIVersion: apiVersion, GeneratedAt: rfc3339(snap.Now),
		Budget:  budgetJSON{DailyUSD: snap.DailyUSD, SpentTodayUSD: math.Round(snap.SpentUSD*1e6) / 1e6},
		Targets: []targetAPI{}}
	for _, t := range snap.Targets {
		a := targetAPI{Name: t.Name, Format: t.Format, Model: t.Model, LastHour: lastHourJSON{Probes: t.Recent.Probes}}
		if t.Recent.Probes > 0 {
			a.LastHour.ErrorRatio = ptr(t.Recent.ErrPct / 100)
		}
		if t.Recent.TTFT > 0 {
			a.LastHour.TTFTP50Seconds = ptr(float64(t.Recent.TTFT.Milliseconds()) / 1e3)
		}
		if t.Recent.Cache != "-" {
			a.LastHour.Cache = ptr(t.Recent.Cache)
		}
		if s := t.State; s != nil {
			a.Scenario, a.State, a.Since, a.Cause = ptr(s.Scenario), ptr(s.State), ptr(rfc3339(s.Since)), orNull(s.Cause)
			if s.IncidentID != 0 {
				a.IncidentID = ptr(s.IncidentID)
			}
			a.LastProbe = &lastProbeJSON{At: rfc3339(s.Last), State: s.LastState, Detail: orNull(s.LastDetail)}
		}
		out.Targets = append(out.Targets, a)
	}
	return out
}

func incidentsOf(snap snapshot, openOnly bool) incidentsResponse {
	out := incidentsResponse{APIVersion: apiVersion, GeneratedAt: rfc3339(snap.Now), Since: rfc3339(snap.Since),
		Incidents: []incidentAPI{}}
	for _, inc := range snap.Incidents {
		open := inc.Closed.IsZero()
		if openOnly && !open {
			continue
		}
		a := incidentAPI{ID: inc.ID, Target: inc.Target, Scenario: inc.Scenario, Status: "open", State: inc.State,
			Cause: inc.Cause, Detail: orNull(inc.Detail), OpenedAt: rfc3339(inc.Opened), UpdatedAt: rfc3339(inc.Updated)}
		end := snap.Now
		if !open {
			end = inc.Closed
			a.ClosedAt, a.CloseCause, a.Status = ptr(rfc3339(inc.Closed)), ptr(inc.CloseCause), "closed"
			if inc.Resolved {
				a.Status = "resolved"
			}
		}
		a.DurationSeconds = int64(max(end.Sub(inc.Opened), 0) / time.Second)
		out.Incidents = append(out.Incidents, a)
	}
	return out
}

func ptr[T any](v T) *T { return &v }

// orNull is nil for an empty string: a field with no value is null.
func orNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
