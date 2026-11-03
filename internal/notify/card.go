package notify

import (
	"fmt"
	"strings"

	"github.com/piplon-sh/piplon/internal/store"
)

// State colours, from docs/DESIGN.md.
const (
	colourFailure = "#D9480F" // down, errors, gone
	colourWarning = "#D98B0F" // slow, cache_miss, refused
	colourOK      = "#2F9E5B" // back to ok
	colourClosed  = "#8A8A85" // closed without resolution
)

// card is a notification laid out for a chat channel, before any markup;
// Slack and Discord each render it in their own format.
type card struct {
	Colour   string // #RRGGBB
	Title    string // with the emoji matching Colour
	Cause    string // empty for a resolution
	Measured string // empty for a resolution, or when nothing was measured
	Line     string // a resolution's one line in place of Cause and Measured
	Footer   string // incident number, scenario, and when
}

func cardFor(n store.Notification) card {
	inc := n.Incident
	c := card{Colour: colourFailure, Title: fmt.Sprintf("🔴 %s is %s", inc.Target, inc.State),
		Cause: inc.Cause, Measured: inc.Detail,
		Footer: fmt.Sprintf("#%d · %s · since %s", inc.ID, inc.Scenario, clock(inc.Opened))}
	switch {
	case n.Event == store.EventClosed && inc.Resolved:
		c.Colour, c.Title = colourOK, fmt.Sprintf("🟢 %s is back to ok", inc.Target)
		// Once resolved the advice after the colon is moot; the cause stays.
		cause, _, _ := strings.Cut(inc.Cause, ": ")
		c.Cause, c.Measured = "", ""
		c.Line = fmt.Sprintf("Was %s for %s · cause: %s", inc.State, lasted(inc), cause)
		c.Footer = fmt.Sprintf("#%d · %s · %s", inc.ID, inc.Scenario, period(inc)) // the duration is in Line
	case n.Event == store.EventClosed:
		c.Colour, c.Title = colourClosed, fmt.Sprintf("⚪ %s: incident closed without resolution", inc.Target)
		c.Footer = fmt.Sprintf("#%d · %s · %s · %s · %s", inc.ID, inc.Scenario, lasted(inc), period(inc), inc.CloseCause)
	case inc.State == "slow" || inc.State == "cache_miss" || inc.State == "refused":
		c.Colour, c.Title = colourWarning, fmt.Sprintf("🟠 %s is %s", inc.Target, inc.State)
	}
	return c
}

// inlineCode wraps s as inline code; a backtick inside would end it early.
func inlineCode(s string) string { return "`" + strings.ReplaceAll(s, "`", "'") + "`" }
