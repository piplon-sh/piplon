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

// OpenAI probes an OpenAI-compatible Chat Completions endpoint with a
// streamed request: OpenAI, OpenRouter, Groq, Together, LiteLLM, vLLM and
// Ollama's OpenAI API all speak it.
type OpenAI struct {
	URL    string // base URL including the version, for example https://api.openai.com/v1
	Model  string
	Key    config.Secret // empty sends no Authorization header
	Client *http.Client  // nil uses a client bounded only by the probe context
}

type openaiRequest struct {
	Model         string          `json:"model"`
	Messages      []openaiMessage `json:"messages"`
	Tools         []openaiTool    `json:"tools,omitempty"`
	MaxTokens     int             `json:"max_tokens"`
	Temperature   float64         `json:"temperature"`
	Stream        bool            `json:"stream"`
	StreamOptions streamOptions   `json:"stream_options"`
}

// streamOptions asks for a final usage chunk, the only place token counts
// appear in a streamed answer.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiTool struct {
	Type     string         `json:"type"`
	Function openaiFunction `json:"function"`
}

type openaiFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type openaiChunk struct {
	Choices []struct {
		Delta struct {
			Content   string          `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *apiError `json:"error"`
}

// Probe runs every turn of s as one conversation, with the same stopping
// rules as Anthropic.Probe. Prompt caching is automatic on this API, so no
// cache markers are sent; cached prompt tokens are reported as cache reads.
func (o *OpenAI) Probe(ctx context.Context, s *scenario.Scenario) Result {
	var res Result
	msgs := []openaiMessage{{Role: "system", Content: s.SystemPrompt()}}
	for _, st := range s.Turns {
		msgs = append(msgs, openaiMessage{Role: st.Role, Content: st.Content})
		turn, reply := o.turn(ctx, s, msgs)
		turn.ExpectOK = turn.Error == "" && strings.Contains(reply, st.Expect.Contains)
		res.Turns = append(res.Turns, turn)
		if turn.Error != "" {
			break
		}
		msgs = append(msgs, openaiMessage{Role: "assistant", Content: reply})
	}
	return res
}

func (o *OpenAI) request(s *scenario.Scenario, msgs []openaiMessage) openaiRequest {
	tools := make([]openaiTool, len(s.Tools))
	for i, t := range s.Tools {
		tools[i] = openaiTool{Type: "function", Function: openaiFunction{Name: t.Name, Description: t.Description, Parameters: t.Parameters}}
	}
	return openaiRequest{
		Model:         o.Model,
		Messages:      msgs,
		Tools:         tools,
		MaxTokens:     s.Limits.MaxOutputTokens,
		Temperature:   s.Limits.Temperature,
		Stream:        true,
		StreamOptions: streamOptions{IncludeUsage: true},
	}
}

func (o *OpenAI) turn(ctx context.Context, s *scenario.Scenario, msgs []openaiMessage) (Turn, string) {
	turn := Turn{Start: time.Now()}
	body, err := json.Marshal(o.request(s, msgs))
	if err != nil {
		turn.Error = "encode request: " + err.Error()
		return turn, ""
	}
	header := http.Header{}
	if o.Key != "" {
		header.Set("Authorization", "Bearer "+o.Key.Reveal())
	}
	resp, wrote, err := post(ctx, o.Client, strings.TrimRight(o.URL, "/")+"/chat/completions", header, body, turn.Start)
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
	reply := readOpenAIStream(ctx, resp.Body, &turn, wrote)
	turn.Total = time.Since(turn.Start)
	return turn, reply
}

// readOpenAIStream fills turn from the chunk stream and returns the text.
// TTFT stops at the first chunk carrying content or a tool call, not at the
// role-only chunk servers send first.
func readOpenAIStream(ctx context.Context, body io.Reader, turn *Turn, wrote func() time.Time) string {
	var text strings.Builder
	var gotDelta, done, toolCall bool
	err := readSSE(body, func(_ string, data []byte) bool {
		if string(data) == "[DONE]" {
			done = true
			return false
		}
		var ch openaiChunk
		if err := json.Unmarshal(data, &ch); err != nil {
			turn.Error = "unreadable stream chunk"
			return false
		}
		if ch.Error != nil {
			turn.ProviderError = ch.Error.kind()
			turn.Error = providerMessage(ch.Error.kind(), ch.Error.Message)
			return false
		}
		for _, c := range ch.Choices {
			hasToolCall := len(c.Delta.ToolCalls) > 0 && string(c.Delta.ToolCalls) != "null"
			toolCall = toolCall || hasToolCall
			if !gotDelta && (c.Delta.Content != "" || hasToolCall) {
				gotDelta = true
				turn.TTFT = time.Since(wrote())
			}
			text.WriteString(c.Delta.Content)
		}
		if u := ch.Usage; u != nil {
			turn.CacheReadTokens = u.PromptTokensDetails.CachedTokens
			turn.InputTokens = u.PromptTokens - u.PromptTokensDetails.CachedTokens
			turn.OutputTokens = u.CompletionTokens
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
		turn.Error = "stream ended before [DONE]"
	case text.Len() == 0 && toolCall:
		turn.Error = errToolCall
	case text.Len() == 0:
		turn.Error = "empty reply"
	}
	return text.String()
}
