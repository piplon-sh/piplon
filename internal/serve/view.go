package serve

import (
	"fmt"
	"strings"
	"time"

	"github.com/piplon-sh/piplon/internal/analyze"
	"github.com/piplon-sh/piplon/internal/store"
)

// view is what the dashboard shows. Every string is final text; the
// templates only lay it out, and html/template escapes it.
type view struct {
	Clock        string // 14:31 UTC
	Updated      string // updated 12 s ago, from the latest probe of any target
	Probed       bool   // some target has been probed; until then the banner waits
	Spent, Daily string // USD today, and budget.daily_usd
	Problems     []problem
	Targets      []targetRow
	Incidents    []incidentRow
}

// problem is one line of the banner: a target not ok.
type problem struct {
	Target, State, Class, Cause, Since string
}

type targetRow struct {
	Name, State, Class, TTFT, Err, Cache, Last string
}

type incidentRow struct {
	ID, When, Target, Cause, Detail, Duration, State, Class string
}

// class is the colour family of a state: ok, warn or bad.
func class(state string) string {
	switch analyze.State(state) {
	case analyze.OK:
		return "ok"
	case analyze.Slow, analyze.CacheMiss, analyze.Refused:
		return "warn"
	default:
		return "bad"
	}
}

// incidentRowOf shows an incident: its state while open, then resolved, or
// closed for an administrative close, which is never shown as resolved. The
// cause is cut before its colon, as in the resolved notifications: the
// advice after it is left to the banner while the incident is open.
func incidentRowOf(inc store.Incident, now time.Time) incidentRow {
	cause, _, _ := strings.Cut(inc.Cause, ": ")
	row := incidentRow{ID: fmt.Sprintf("#%d", inc.ID), When: stamp(inc.Opened), Target: inc.Target, Cause: cause, Detail: inc.Detail}
	switch {
	case inc.Closed.IsZero():
		row.Duration = span(now.Sub(inc.Opened)) + " · ongoing"
		row.State, row.Class = inc.State, class(inc.State)
	case inc.Resolved:
		row.Duration = span(inc.Closed.Round(time.Minute).Sub(inc.Opened.Round(time.Minute)))
		row.State, row.Class = "resolved", "ok"
	default:
		row.Duration = span(inc.Closed.Round(time.Minute).Sub(inc.Opened.Round(time.Minute)))
		row.State, row.Class = "closed", "muted"
	}
	return row
}

// stamp is a time to the minute in UTC: 2026-11-03 09:02 UTC.
func stamp(t time.Time) string { return t.UTC().Round(time.Minute).Format("2006-01-02 15:04 UTC") }

// since is 09:02 UTC today, else the full stamp.
func since(t, now time.Time) string {
	if t.UTC().Round(time.Minute).Format(time.DateOnly) == now.UTC().Format(time.DateOnly) {
		return t.UTC().Round(time.Minute).Format("15:04 UTC")
	}
	return stamp(t)
}

// span is a duration read at a glance: 40 s, 14 min, 2 h 05 min.
func span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d h %02d min", int(d.Hours()), int(d.Minutes())%60)
	}
}

// ago is how long before now t was, as the terminal table shows it.
func ago(t, now time.Time) string {
	switch d := max(now.Sub(t), 0); {
	case d < time.Minute:
		return fmt.Sprintf("%d s ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d m ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
}

func millis(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d ms", d.Milliseconds())
}

func percent(r analyze.Recent) string {
	if r.Probes == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f %%", r.ErrPct)
}
