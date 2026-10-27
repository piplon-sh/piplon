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
	"sync/atomic"
	"time"

	"github.com/piplon-sh/piplon/internal/version"
)

const (
	maxErrorBody    = 64 << 10
	maxErrorMessage = 200
)

// errToolCall is recorded when a turn answers with a tool call and no text:
// the scenario asks for text, and there is nothing to carry forward.
const errToolCall = "tool call instead of text"

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
func readHTTPError(resp *http.Response, turn *Turn) {
	var body struct {
		Error apiError `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if json.Unmarshal(raw, &body) != nil || body.Error.kind() == "" {
		turn.Error = fmt.Sprintf("http %d", resp.StatusCode)
		return
	}
	turn.ProviderError = body.Error.kind()
	turn.Error = fmt.Sprintf("http %d: %s", resp.StatusCode, providerMessage(body.Error.kind(), body.Error.Message))
}

func providerMessage(kind, msg string) string {
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
