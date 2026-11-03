package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/piplon-sh/piplon/internal/store"
)

// Text is the human message for a notification: what happened to which
// target, the probable cause and what was measured. An administrative close
// says the incident closed without resolution and never that it resolved.
func Text(n store.Notification) string {
	inc := n.Incident
	ref := fmt.Sprintf("Incident #%d · %s", inc.ID, inc.Scenario)
	switch {
	case n.Event == store.EventOpened:
		return lines(fmt.Sprintf("piplon · %s is %s", inc.Target, inc.State),
			"Probable cause: "+inc.Cause, "Measured: "+inc.Detail,
			fmt.Sprintf("%s · opened %s", ref, clock(inc.Opened)))
	case n.Event == store.EventUpdated:
		return lines(fmt.Sprintf("piplon · %s is now %s", inc.Target, inc.State),
			"Probable cause: "+inc.Cause, "Measured: "+inc.Detail,
			fmt.Sprintf("%s · open since %s", ref, clock(inc.Opened)))
	case inc.Resolved:
		return lines(fmt.Sprintf("piplon · %s is back to ok", inc.Target),
			fmt.Sprintf("%s · resolved after %s · was %s", ref, lasted(inc), inc.State))
	default:
		return lines(fmt.Sprintf("piplon · incident #%d on %s closed without resolution", inc.ID, inc.Target),
			fmt.Sprintf("Reason: %s · was %s since %s", inc.CloseCause, inc.State, clock(inc.Opened)))
	}
}

// lines joins the non-empty parts of a message, dropping a label with no
// value after it.
func lines(parts ...string) string {
	var out []string
	for _, p := range parts {
		if !strings.HasSuffix(p, ": ") {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

// clock and period show times rounded to the nearest minute, and lasted is
// the difference of those rounded times, so "4 min" always matches the
// 12:20 → 12:24 printed next to it.
func clock(t time.Time) string { return t.UTC().Round(time.Minute).Format("2006-01-02 15:04 UTC") }

// period is when an incident was open: 2026-11-03 09:02 → 09:16 UTC, with the
// close's date only when it is another day.
func period(inc store.Incident) string {
	opened, closed := inc.Opened.UTC().Round(time.Minute), inc.Closed.UTC().Round(time.Minute)
	end := closed.Format("15:04")
	if closed.Format("2006-01-02") != opened.Format("2006-01-02") {
		end = closed.Format("2006-01-02 15:04")
	}
	return opened.Format("2006-01-02 15:04") + " → " + end + " UTC"
}

// lasted is how long a closed incident was open. Under a minute, when both
// ends round to the same minute, it counts seconds instead.
func lasted(inc store.Incident) string {
	if d := inc.Closed.Round(time.Minute).Sub(inc.Opened.Round(time.Minute)); d >= time.Minute {
		return span(d)
	}
	return span(inc.Closed.Sub(inc.Opened))
}

// span is a duration an operator reads at a glance: 40 s, 14 min, 2 h 05 min.
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
