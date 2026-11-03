// Package notify delivers incident events from the store's outbox to Slack,
// Discord and webhooks.
//
// A Channel has a Name, used in the outbox and the logs and never secret,
// and Sends one store.Notification. Slack posts to an incoming webhook one
// attachment whose colour bar is the state (red for down, errors and gone,
// orange for slow, cache_miss and refused, green back to ok, grey for a close
// without resolution), holding Block Kit blocks: a bold title whose emoji
// matches the bar (🔴, 🟠, 🟢, ⚪), a section for the Cause, a full-width
// section for the Measured detail as inline code, and a grey context with the
// incident number, the scenario and when it opened, or for a close how long it
// lasted and from when to when. A resolution replaces Cause and Measured with
// one line, "Was <state> for <duration> · cause: <cause>", the cause without
// the advice after its colon, and its context
// gives only from when to when. Text is the
// attachment's fallback, what mobile notifications show; there is no top-level
// text, which Slack would print above the attachment. Times are rounded to the
// nearest minute and durations are the difference of the rounded times. &, <
// and > are escaped. Incoming webhooks cannot thread, so every message names
// its incident number.
//
// Discord posts to a Discord webhook one embed laid out from the same card as
// the Slack message: the state's colour, the title with its emoji, a Cause
// field and a full-width Measured field as inline code, or a resolution's one
// line as the description, and the same footer. It sends no content, which
// Discord would print above the embed, and disables mentions, so an
// "@everyone" quoted in a provider message pings nobody. Markdown characters
// are escaped and every part is cut to Discord's embed limits.
//
// Webhook posts a "piplon.incident.v1" JSON document: the notification id, stable across retries because delivery is
// at least once, the event, its time, Text, and the incident as the event
// left it (closed is null while open, resolved is false for an
// administrative close). Errors never contain the channel URL, which holds
// its token.
//
// Text names the target, its state, the probable cause and what was measured.
// A close that is a resolution says the target is back to ok and for how
// long the incident lasted; an administrative close says the incident closed
// without resolution and why, and never reads as resolved.
//
// Dispatcher drains the outbox on Run's start, on every Wake and every 5 s,
// one round after another, since a delivery can release the next event of
// the same incident. A failed send is retried after 30 s, doubling up to 30
// min; an event still undelivered 24 h after it happened is abandoned and
// logged as an error, and so is one for a channel no longer configured. A send
// cut short by shutdown is not counted and goes out at the next start.
package notify
