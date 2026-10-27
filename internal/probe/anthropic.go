package probe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/scenario"
)

const anthropicVersion = "2023-06-01"

// Anthropic probes the Anthropic Messages API with a streamed request.
type Anthropic struct {
	URL    string // base URL, for example https://api.anthropic.com
	Model  string
	Key    config.Secret
	Client *http.Client // nil uses a client bounded only by the probe context
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature"`
	Stream      bool               `json:"stream"`
	System      []anthropicText    `json:"system"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
}

type anthropicText struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
}

var ephemeral = &cacheControl{Type: "ephemeral"}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content []anthropicText `json:"content"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

// anthropicEvent covers every streamed event type piplon reads.
type anthropicEvent struct {
	Type    string `json:"type"`
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
	} `json:"content_block"`
	Delta struct {
		Text string `json:"text"`
	} `json:"delta"`
	Usage anthropicUsage `json:"usage"`
	Error apiError       `json:"error"`
}

// Probe runs every turn of s as one conversation. It stops at the first turn
// that fails to complete; a turn whose answer misses its expectation is
// recorded and the conversation goes on, so cache reads stay measurable.
func (a *Anthropic) Probe(ctx context.Context, s *scenario.Scenario) Result {
	var res Result
	var msgs []anthropicMessage
	for i, st := range s.Turns {
		user := anthropicText{Type: "text", Text: st.Content}
		if i == 0 {
			user.CacheControl = ephemeral
		}
		msgs = append(msgs, anthropicMessage{Role: st.Role, Content: []anthropicText{user}})
		turn, reply := a.turn(ctx, s, msgs)
		turn.ExpectOK = turn.Error == "" && strings.Contains(reply, st.Expect.Contains)
		res.Turns = append(res.Turns, turn)
		if turn.Error != "" {
			break
		}
		msgs = append(msgs, anthropicMessage{Role: "assistant", Content: []anthropicText{{Type: "text", Text: reply}}})
	}
	return res
}

func (a *Anthropic) request(s *scenario.Scenario, msgs []anthropicMessage) anthropicRequest {
	tools := make([]anthropicTool, len(s.Tools))
	for i, t := range s.Tools {
		tools[i] = anthropicTool{Name: t.Name, Description: t.Description, InputSchema: t.Parameters}
	}
	return anthropicRequest{
		Model:       a.Model,
		MaxTokens:   s.Limits.MaxOutputTokens,
		Temperature: s.Limits.Temperature,
		Stream:      true,
		System:      []anthropicText{{Type: "text", Text: s.SystemPrompt(), CacheControl: ephemeral}},
		Tools:       tools,
		Messages:    msgs,
	}
}

// turn sends one request and measures it. It returns the answer text.
func (a *Anthropic) turn(ctx context.Context, s *scenario.Scenario, msgs []anthropicMessage) (Turn, string) {
	turn := Turn{Start: time.Now()}
	body, err := json.Marshal(a.request(s, msgs))
	if err != nil {
		turn.Error = "encode request: " + err.Error()
		return turn, ""
	}
	header := http.Header{}
	header.Set("X-Api-Key", a.Key.Reveal())
	header.Set("Anthropic-Version", anthropicVersion)
	resp, wrote, err := post(ctx, a.Client, strings.TrimRight(a.URL, "/")+"/v1/messages", header, body, turn.Start)
	if err != nil {
		turn.Total = time.Since(turn.Start)
		turn.Error = transportError(ctx, err)
		return turn, ""
	}
	defer func() { _ = resp.Body.Close() }()
	turn.HTTPStatus = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		readHTTPError(resp, &turn)
		turn.Total = time.Since(turn.Start)
		return turn, ""
	}
	reply := readAnthropicStream(ctx, resp.Body, &turn, wrote)
	turn.Total = time.Since(turn.Start)
	return turn, reply
}

// readAnthropicStream fills turn from the event stream and returns the text.
func readAnthropicStream(ctx context.Context, body io.Reader, turn *Turn, wrote func() time.Time) string {
	var text strings.Builder
	var gotDelta, done, toolUse bool
	err := readSSE(body, func(_ string, data []byte) bool {
		var ev anthropicEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			turn.Error = "unreadable stream event"
			return false
		}
		switch ev.Type {
		case "message_start":
			u := ev.Message.Usage
			turn.InputTokens, turn.CacheReadTokens, turn.CacheWriteTokens = u.InputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens
			turn.OutputTokens = u.OutputTokens
		case "content_block_start":
			toolUse = toolUse || ev.ContentBlock.Type == "tool_use"
		case "content_block_delta":
			if !gotDelta {
				gotDelta = true
				turn.TTFT = time.Since(wrote())
			}
			text.WriteString(ev.Delta.Text)
		case "message_delta":
			turn.OutputTokens = ev.Usage.OutputTokens // cumulative
		case "message_stop":
			done = true
			return false
		case "error":
			turn.ProviderError = ev.Error.kind()
			turn.Error = providerMessage(ev.Error.kind(), ev.Error.Message)
			return false
		}
		return true
	})
	switch {
	case turn.Error != "":
	case err != nil:
		turn.Error = transportError(ctx, err)
		turn.StreamCut = ctx.Err() == nil
	case !done:
		turn.StreamCut = true
		turn.Error = "stream ended before message_stop"
	case text.Len() == 0 && toolUse:
		turn.Error = errToolCall
	case text.Len() == 0:
		turn.Error = "empty reply"
	}
	return text.String()
}
