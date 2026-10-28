package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/piplon-sh/piplon/internal/analyze"
	"github.com/piplon-sh/piplon/internal/probe"
	"github.com/piplon-sh/piplon/internal/store"
)

// Display states besides the analyze ones: no probe yet, or probing paused
// by the daily budget, which is not an outage.
const (
	statePending = "…"
	stateBudget  = "budget"
)

// row is one target's line in the live table.
type row struct {
	Target string
	State  string
	Cache  string        // hit, miss or - for the last probe; n/a if it never cached
	Note   string        // why the target is not ok, or the state it may be moving to
	Last   time.Time     // zero before the first tick
	TTFT   time.Duration // p50 over the last hour; 0 when unknown
	ErrPct float64       // share of failed probes over the last hour
	Probes int           // probes over the last hour
}

// apply sets State, Cache, Note and Last from one outcome and the target's
// status after it. While another state is pending, NOTE says which and how
// many probes in a row have seen it.
func (r *row) apply(o probe.Outcome, st analyze.Status) {
	r.Last = o.Start
	switch o.Skipped {
	case probe.SkipBudget:
		r.State, r.Cache, r.Note = stateBudget, "-", "daily budget reached"
		return
	case probe.SkipOverlap:
		r.Note = "previous probe still running"
		return
	}
	r.State, r.Cache, r.Note = string(st.State), st.Last.Cache, st.Last.Detail
	if st.Pending != "" {
		r.Note = fmt.Sprintf("%s %d/%d", st.Pending, st.Count, analyze.Confirm)
		if st.Last.Detail != "" {
			r.Note += " · " + st.Last.Detail
		}
	}
}

// applyStats sets TTFT, ErrPct and Probes from the last hour of samples. A
// refusal is a state of its own, not an error. A miss on a target that read
// no cache all hour shows as n/a: it never cached, it did not stop.
func (r *row) applyStats(samples []store.Sample) {
	failed := map[string]bool{}
	var ttfts []time.Duration
	cached := false
	for _, s := range samples {
		cached = cached || s.CacheReadTokens > 0
		bad := (s.Error != "" && !(probe.Turn{Error: s.Error}).Refused()) || (s.Error == "" && !s.ExpectOK)
		failed[s.ProbeID] = failed[s.ProbeID] || bad
		if s.Error == "" && s.TTFT > 0 {
			ttfts = append(ttfts, s.TTFT)
		}
	}
	r.Probes, r.ErrPct, r.TTFT = len(failed), 0, 0
	for _, f := range failed {
		if f {
			r.ErrPct++
		}
	}
	if r.Probes > 0 {
		r.ErrPct = 100 * r.ErrPct / float64(r.Probes)
	}
	if len(ttfts) > 0 {
		slices.Sort(ttfts)
		r.TTFT = ttfts[len(ttfts)/2]
	}
	if r.Cache == "miss" && !cached {
		r.Cache = "n/a"
	}
}

var (
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))

	stateStyles = map[string]lipgloss.Style{
		string(analyze.OK):        green,
		string(analyze.CacheMiss): yellow,
		string(analyze.Slow):      yellow,
		string(analyze.Refused):   yellow,
		string(analyze.Errors):    red,
		string(analyze.Down):      red,
		string(analyze.Gone):      red,
		stateBudget:               yellow,
	}
)

// minNote is the narrowest NOTE column worth showing; gap is the space
// between two columns.
const (
	minNote = 20
	gap     = 2
)

// render draws the table within width columns. NOTE takes what is left and
// is truncated to it; below minNote it is hidden, then LAST.
func render(rows []row, width int, now time.Time) string {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Target < rows[j].Target })
	six := renderCols(rows, 6, now)
	if lipgloss.Width(six) > width {
		return renderCols(rows, 5, now)
	}
	room := width - lipgloss.Width(six) - gap
	if room < minNote || !slices.ContainsFunc(rows, func(r row) bool { return r.Note != "" }) {
		return six
	}
	cut := slices.Clone(rows)
	for i := range cut {
		cut[i].Note = truncate(cut[i].Note, room)
	}
	return renderCols(cut, 7, now)
}

// truncate cuts s to n runes, ending with an ellipsis when it was longer.
func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n && n > 0 {
		return string(r[:n-1]) + "…"
	}
	return s
}

func renderCols(rows []row, cols int, now time.Time) string {
	headers := []string{"TARGET", "STATE", "TTFT p50", "ERR 1h", "CACHE", "LAST", "NOTE"}[:cols]
	cells := make([][]string, len(rows))
	for i, r := range rows {
		cells[i] = []string{r.Target, r.State, ms(r.TTFT), pct(r), r.Cache, ago(r.Last, now), r.Note}[:cols]
	}
	t := table.New().Headers(headers...).Rows(cells...).
		Border(lipgloss.HiddenBorder()).BorderTop(false).BorderBottom(false).
		BorderLeft(false).BorderRight(false).BorderHeader(false).
		StyleFunc(func(r, c int) lipgloss.Style {
			s := lipgloss.NewStyle().PaddingRight(1)
			if r >= 0 && c == 1 {
				return stateStyles[rows[r].State].PaddingRight(1)
			}
			return s
		})
	lines := strings.Split(t.Render(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func ms(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d ms", d.Milliseconds())
}

func pct(r row) string {
	if r.Probes == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f %%", r.ErrPct)
}

func ago(t, now time.Time) string {
	switch d := now.Sub(t); {
	case t.IsZero():
		return "-"
	case d < time.Minute:
		return fmt.Sprintf("%d s ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d m ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
}
