package notify

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/store"
)

// Slack posts to a Slack incoming webhook: one attachment whose colour bar
// is the state, holding Block Kit blocks, and Text as the plain fallback that
// mobile notifications show. Incoming webhooks cannot thread, so every
// message names its incident number.
type Slack struct {
	URL    config.Secret
	Client *http.Client // nil uses http.DefaultClient
}

// Name implements Channel.
func (Slack) Name() string { return "slack" }

// Send implements Channel.
func (s Slack) Send(ctx context.Context, n store.Notification) error {
	return post(ctx, s.Client, s.URL, slackMessage(n))
}

// State colours, from docs/DESIGN.md.
const (
	colourFailure = "#D9480F" // down, errors, gone
	colourWarning = "#D98B0F" // slow, cache_miss, refused
	colourOK      = "#2F9E5B" // back to ok
	colourClosed  = "#8A8A85" // closed without resolution
)

// slackDoc has no top-level text: Slack would show it above the attachment.
// The attachment's fallback is what mobile notifications show instead.
type slackDoc struct {
	Attachments []slackAttachment `json:"attachments"`
}

type slackAttachment struct {
	Color    string       `json:"color"`
	Fallback string       `json:"fallback"`
	Blocks   []slackBlock `json:"blocks"`
}

type slackBlock struct {
	Type     string       `json:"type"`
	Text     *slackText   `json:"text,omitempty"`
	Elements []*slackText `json:"elements,omitempty"`
}

type slackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func section(s string) slackBlock {
	return slackBlock{Type: "section", Text: &slackText{Type: "mrkdwn", Text: s}}
}

func slackMessage(n store.Notification) slackDoc {
	inc := n.Incident
	esc := slackEscape.Replace
	colour, title := colourFailure, fmt.Sprintf("🔴 %s is %s", inc.Target, inc.State)
	footer := fmt.Sprintf("#%d · %s · since %s", inc.ID, inc.Scenario, clock(inc.Opened))
	var body []slackBlock
	if inc.Cause != "" {
		body = append(body, section("*Cause*\n"+esc(inc.Cause)))
	}
	if inc.Detail != "" {
		// A section of its own so it gets the full width; inside inline code
		// a backtick would end it early.
		body = append(body, section("*Measured*\n`"+strings.ReplaceAll(esc(inc.Detail), "`", "'")+"`"))
	}
	switch {
	case n.Event == store.EventClosed && inc.Resolved:
		colour, title = colourOK, fmt.Sprintf("🟢 %s is back to ok", inc.Target)
		footer = fmt.Sprintf("#%d · %s · %s", inc.ID, inc.Scenario, period(inc)) // the duration is in the line above
		// Once resolved the advice after the colon is moot; the cause stays.
		cause, _, _ := strings.Cut(inc.Cause, ": ")
		body = []slackBlock{section(esc(fmt.Sprintf("Was %s for %s · cause: %s", inc.State, lasted(inc), cause)))}
	case n.Event == store.EventClosed:
		colour, title = colourClosed, fmt.Sprintf("⚪ %s: incident closed without resolution", inc.Target)
		footer = fmt.Sprintf("#%d · %s · %s · %s · %s", inc.ID, inc.Scenario, lasted(inc), period(inc), inc.CloseCause)
	case inc.State == "slow" || inc.State == "cache_miss" || inc.State == "refused":
		colour, title = colourWarning, fmt.Sprintf("🟠 %s is %s", inc.Target, inc.State)
	}
	blocks := append([]slackBlock{section("*" + esc(title) + "*")}, body...)
	blocks = append(blocks, slackBlock{Type: "context", Elements: []*slackText{{Type: "mrkdwn", Text: esc(footer)}}})
	return slackDoc{Attachments: []slackAttachment{{Color: colour, Fallback: esc(Text(n)), Blocks: blocks}}}
}

// slackEscape escapes the three characters Slack reserves for its markup.
var slackEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
