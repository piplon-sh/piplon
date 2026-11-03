package notify

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/store"
)

// Discord posts to a Discord webhook one embed laid out like the Slack
// message: the state's colour, the title with its emoji, Cause and Measured
// fields, and the footer. There is no content, which Discord would print
// above the embed. Mentions are disabled, so an "@everyone" quoted in a
// provider message never pings anyone.
type Discord struct {
	URL    config.Secret
	Client *http.Client // nil uses http.DefaultClient
}

// Name implements Channel.
func (Discord) Name() string { return "discord" }

// Send implements Channel.
func (d Discord) Send(ctx context.Context, n store.Notification) error {
	return post(ctx, d.Client, d.URL, discordMessage(n))
}

// Embed limits, in characters.
const (
	discordTitleMax  = 256
	discordFieldMax  = 1024
	discordFooterMax = 2048
)

type discordDoc struct {
	Embeds          []discordEmbed  `json:"embeds"`
	AllowedMentions discordMentions `json:"allowed_mentions"`
}

type discordMentions struct {
	Parse []string `json:"parse"` // empty: nobody is mentioned
}

type discordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Color       int            `json:"color"`
	Fields      []discordField `json:"fields,omitempty"`
	Footer      discordFooter  `json:"footer"`
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type discordFooter struct {
	Text string `json:"text"`
}

func discordMessage(n store.Notification) discordDoc {
	c, esc := cardFor(n), discordEscape.Replace
	e := discordEmbed{Title: cut(esc(c.Title), discordTitleMax), Description: cut(esc(c.Line), discordFieldMax),
		Color: rgb(c.Colour), Footer: discordFooter{Text: cut(c.Footer, discordFooterMax)}} // footers render no markdown
	if c.Cause != "" {
		e.Fields = append(e.Fields, discordField{Name: "Cause", Value: cut(esc(c.Cause), discordFieldMax)})
	}
	if c.Measured != "" {
		// Not inline, so it gets the full width.
		e.Fields = append(e.Fields, discordField{Name: "Measured", Value: inlineCode(cut(c.Measured, discordFieldMax-2))})
	}
	return discordDoc{Embeds: []discordEmbed{e}, AllowedMentions: discordMentions{Parse: []string{}}}
}

// discordEscape keeps Discord's markdown from reading a target name or a
// provider message as formatting.
var discordEscape = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "|", `\|`, "`", "\\`")

// rgb is #RRGGBB as the integer Discord wants.
func rgb(hex string) int {
	v, _ := strconv.ParseInt(strings.TrimPrefix(hex, "#"), 16, 32)
	return int(v)
}

// cut keeps s within n characters, ending with an ellipsis when it was longer.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
