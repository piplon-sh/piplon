package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/version"
)

const (
	maxErrorBody    = 64 << 10
	maxErrorMessage = 200
)

// errToolCall is recorded when a turn answers with a tool call and no text:
// the scenario asks for text, and there is nothing to carry forward.
const errToolCall = "tool call instead of text"

// errRefused starts the error of a turn the model declined to answer, with
// the provider's category after a colon when it gives one ("refused: bio").
// It is not an empty reply: the model did answer, with a refusal.
const errRefused = "refused"

// Refused reports whether the model declined to answer this turn.
func (t Turn) Refused() bool {
	return t.Error == errRefused || strings.HasPrefix(t.Error, errRefused+": ")
}

func refusal(category string) string {
	if category == "" {
		return errRefused
	}
	return errRefused + ": " + category
}

// defaultClient has no timeout of its own: the probe context bounds it.
var defaultClient = &http.Client{}

// post sends a JSON body. Besides the response it returns a function giving
// the instant the request was fully written, where TTFT starts; before that
// instant is known it falls back to start.
func post(ctx context.Context, client *http.Client, url string, header http.Header, body []byte, start time.Time) (*http.Response, func() time.Time, error) {
	// The transport calls WroteRequest from its own goroutine.
	var wrote atomic.Pointer[time.Time]
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) {
		now := time.Now()
		wrote.Store(&now)
	}}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	for name, values := range header {
		req.Header[name] = values
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "piplon/"+version.Version)
	if client == nil {
		client = defaultClient
	}
	resp, err := client.Do(req)
	return resp, func() time.Time {
		if w := wrote.Load(); w != nil {
			return *w
		}
		return start
	}, err
}

// apiError is the error envelope both providers use: {"error": {...}}.
// OpenAI-compatible servers add a code that may be a string, a number or null.
type apiError struct {
	Type    string          `json:"type"`
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
}

// kind is the most specific error name available: the code when it is a
// non-empty string, else the type.
func (e apiError) kind() string {
	var code string
	if json.Unmarshal(e.Code, &code) == nil && code != "" {
		return code
	}
	return e.Type
}

// readHTTPError records a non-200 answer.
func readHTTPError(resp *http.Response, turn *Turn, key config.Secret) {
	var body struct {
		Error apiError `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if json.Unmarshal(raw, &body) != nil || body.Error.kind() == "" {
		turn.Error = fmt.Sprintf("http %d", resp.StatusCode)
		return
	}
	turn.ProviderError = body.Error.kind()
	turn.Error = fmt.Sprintf("http %d: %s", resp.StatusCode, providerMessage(body.Error.kind(), body.Error.Message, key))
}

// keyLike matches what a provider message may quote of a key: a whole key
// by its known prefix, masked or not (sk-, sk-ant-, sk-proj-, gsk_, xai-,
// AIza), or a masked fragment with the characters it leaves visible.
var keyLike = regexp.MustCompile(`(?:sk-|gsk_|xai-|AIza)[A-Za-z0-9_*-]{6,}|\*{4,}[A-Za-z0-9_-]*`)

// minRedactedKey is the shortest key replaced by its exact value; a shorter
// one could match ordinary words of a message.
const minRedactedKey = 8

// redact replaces the target's key, by its exact value, and anything
// key-like in s.
func redact(s string, key config.Secret) string {
	if k := key.Reveal(); len(k) >= minRedactedKey {
		s = strings.ReplaceAll(s, k, "[redacted]")
	}
	return keyLike.ReplaceAllString(s, "[redacted]")
}

// providerMessage names a provider error. The message is redacted before it
// is cut to length, so a cut cannot leave part of a key unmatched: it goes to
// logs, the store and notifications.
func providerMessage(kind, msg string, key config.Secret) string {
	msg = redact(msg, key)
	if r := []rune(msg); len(r) > maxErrorMessage {
		msg = string(r[:maxErrorMessage]) + "…"
	}
	return kind + ": " + msg
}

// transportError names a failure that produced no usable HTTP answer.
func transportError(ctx context.Context, err error) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timeout"
	case errors.Is(ctx.Err(), context.Canceled):
		return "canceled"
	default:
		return err.Error()
	}
}
