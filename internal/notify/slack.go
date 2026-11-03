package notify

import (
	"context"
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
	c, esc := cardFor(n), slackEscape.Replace
	blocks := []slackBlock{section("*" + esc(c.Title) + "*")}
	if c.Line != "" {
		blocks = append(blocks, section(esc(c.Line)))
	}
	if c.Cause != "" {
		blocks = append(blocks, section("*Cause*\n"+esc(c.Cause)))
	}
	if c.Measured != "" {
		// A section of its own so it gets the full width.
		blocks = append(blocks, section("*Measured*\n"+inlineCode(esc(c.Measured))))
	}
	blocks = append(blocks, slackBlock{Type: "context", Elements: []*slackText{{Type: "mrkdwn", Text: esc(c.Footer)}}})
	return slackDoc{Attachments: []slackAttachment{{Color: c.Colour, Fallback: esc(Text(n)), Blocks: blocks}}}
}

// slackEscape escapes the three characters Slack reserves for its markup.
var slackEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
