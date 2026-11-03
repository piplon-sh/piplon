package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/store"
	"github.com/piplon-sh/piplon/internal/version"
)

// Webhook posts a versioned JSON document for every incident event.
type Webhook struct {
	URL    config.Secret
	Client *http.Client // nil uses http.DefaultClient
}

// Name implements Channel.
func (Webhook) Name() string { return "webhook" }

// webhookVersion names the document schema; a breaking change is a new one.
const webhookVersion = "piplon.incident.v1"

type webhookDoc struct {
	Version  string          `json:"version"`
	ID       int64           `json:"id"` // the same across retries: delivery is at least once
	Event    string          `json:"event"`
	At       time.Time       `json:"at"`
	Text     string          `json:"text"`
	Incident webhookIncident `json:"incident"`
}

type webhookIncident struct {
	ID         int64      `json:"id"`
	Target     string     `json:"target"`
	Scenario   string     `json:"scenario"`
	State      string     `json:"state"`
	Cause      string     `json:"cause"`
	Detail     string     `json:"detail"`
	Opened     time.Time  `json:"opened"`
	Updated    time.Time  `json:"updated"`
	Closed     *time.Time `json:"closed"` // null while open
	CloseCause string     `json:"close_cause"`
	Resolved   bool       `json:"resolved"` // false for an administrative close
}

// Send implements Channel.
func (w Webhook) Send(ctx context.Context, n store.Notification) error {
	inc := n.Incident
	doc := webhookDoc{Version: webhookVersion, ID: n.ID, Event: n.Event, At: n.At.UTC(), Text: Text(n),
		Incident: webhookIncident{ID: inc.ID, Target: inc.Target, Scenario: inc.Scenario, State: inc.State,
			Cause: inc.Cause, Detail: inc.Detail, Opened: inc.Opened.UTC(), Updated: inc.Updated.UTC(),
			CloseCause: inc.CloseCause, Resolved: inc.Resolved}}
	if !inc.Closed.IsZero() {
		closed := inc.Closed.UTC()
		doc.Incident.Closed = &closed
	}
	return post(ctx, w.Client, w.URL, doc)
}

// post sends body as JSON. Its errors never contain the URL, which holds the
// channel's token.
func post(ctx context.Context, client *http.Client, to config.Secret, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, to.Reveal(), bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid url")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "piplon/"+version.Version)
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if ue, ok := errors.AsType[*url.Error](err); ok {
			return ue.Err
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	return nil
}
