package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Role says who wrote a message.
type Role string

// The roles of a conversation.
const (
	System    Role = "system"
	User      Role = "user"
	Assistant Role = "assistant"
)

// Image is a picture sent with a message, to a model that reads pictures.
type Image struct {
	MIME string // image/png, image/jpeg ...
	Data []byte
}

// Message is one turn of a conversation.
type Message struct {
	Role      Role    `json:"role"`
	Text      string  `json:"text"`
	Reasoning string  `json:"reasoning,omitempty"` // what a thinking model wrote before answering; shown, never sent back
	Images    []Image `json:"images,omitempty"`
	Media     []Media `json:"media,omitempty"` // what a model made for this message, kept in the assets folder
	// Attached are the user's pictures, by file; they are read into Images when the message is sent.
	Attached []Media `json:"attached,omitempty"`
	Stats    *Stats  `json:"stats,omitempty"` // what the answer cost, for an assistant's message
	// LeftOut is how many earlier messages this answer was written without, as the model has no room for them.
	LeftOut int `json:"leftOut,omitempty"`
}

// Options steer an answer; zero values leave the choice to the model.
type Options struct {
	Temperature *float64
	MaxTokens   int
}

// Chunk is a piece of an answer as it arrives.
type Chunk struct {
	Text      string
	Reasoning string
}

// Stats says what an answer cost.
type Stats struct {
	PromptTokens     int
	CompletionTokens int
	Elapsed          time.Duration
	// TokensPerSecond is the speed the model wrote at; the engine's own figure when it gives one.
	TokensPerSecond float64
}

// Endpoint is a place that answers chat requests the OpenAI way: a model server on this computer, or an online service.
type Endpoint struct {
	URL    string // the address "/chat/completions" is added to
	Key    string // sent as a bearer token when not empty
	Model  string // the model's name there
	Client *http.Client
	// Local marks a model server of this computer, which can be asked for more than a service accepts.
	Local bool
}

// ErrNoKey means an online model was asked without an API key being entered.
var ErrNoKey = errors.New("enter this service's API key in Settings → AI models first")

// Chat sends the conversation and calls on with each piece of the answer as it arrives. It returns when the answer
// is complete, or ctx is done.
func (e Endpoint) Chat(ctx context.Context, messages []Message, opt Options, on func(Chunk)) (Stats, error) {
	body := map[string]any{"model": e.Model, "stream": true, "messages": wireMessages(messages)}
	if opt.Temperature != nil {
		body["temperature"] = *opt.Temperature
	}
	if opt.MaxTokens > 0 {
		body["max_tokens"] = opt.MaxTokens
	}
	if e.Local {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return Stats{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(e.URL, "/")+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return Stats{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if e.Key != "" {
		req.Header.Set("Authorization", "Bearer "+e.Key)
	}
	client := e.Client
	if client == nil {
		client = http.DefaultClient
	}
	began := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Stats{}, ctx.Err()
		}
		return Stats{}, fmt.Errorf("could not reach the model: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Stats{}, refusal(resp)
	}
	stats, err := readStream(ctx, resp.Body, on)
	stats.Elapsed = time.Since(began)
	if stats.TokensPerSecond == 0 && stats.CompletionTokens > 0 && stats.Elapsed > 0 {
		stats.TokensPerSecond = float64(stats.CompletionTokens) / stats.Elapsed.Seconds()
	}
	return stats, err
}

// Complete asks for one whole answer, for a use that has no window to show it arriving in.
func (e Endpoint) Complete(ctx context.Context, messages []Message, opt Options) (string, error) {
	var text strings.Builder
	if _, err := e.Chat(ctx, messages, opt, func(c Chunk) { text.WriteString(c.Text) }); err != nil {
		return "", err
	}
	return strings.TrimSpace(text.String()), nil
}

// wireMessages are the messages as the interface takes them: plain text, or text with pictures.
func wireMessages(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		if m.Text == "" && len(m.Images) == 0 {
			continue // what a model made as a file has no words to send back
		}
		if len(m.Images) == 0 {
			out = append(out, map[string]any{"role": m.Role, "content": m.Text})
			continue
		}
		parts := []map[string]any{}
		for _, img := range m.Images {
			url := "data:" + img.MIME + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
		}
		if m.Text != "" {
			parts = append(parts, map[string]any{"type": "text", "text": m.Text})
		}
		out = append(out, map[string]any{"role": m.Role, "content": parts})
	}
	return out
}

// streamEvent is one event of a streamed answer.
type streamEvent struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Timings *struct {
		PredictedPerSecond float64 `json:"predicted_per_second"`
	} `json:"timings"`
	Error json.RawMessage `json:"error"`
}

// readStream reads server-sent events until the answer ends.
func readStream(ctx context.Context, r io.Reader, on func(Chunk)) (Stats, error) {
	var stats Stats
	var pieces int
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadString('\n')
		if data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:"); ok {
			data = strings.TrimSpace(data)
			if data == "[DONE]" {
				return finish(stats, pieces), nil
			}
			var ev streamEvent
			if json.Unmarshal([]byte(data), &ev) == nil {
				if len(ev.Error) > 0 {
					return stats, errors.New(errorText(ev.Error))
				}
				for _, c := range ev.Choices {
					chunk := Chunk{Text: c.Delta.Content, Reasoning: c.Delta.ReasoningContent + c.Delta.Reasoning}
					if chunk != (Chunk{}) {
						pieces++
						on(chunk)
					}
				}
				if ev.Usage != nil {
					stats.PromptTokens, stats.CompletionTokens = ev.Usage.PromptTokens, ev.Usage.CompletionTokens
				}
				if ev.Timings != nil && ev.Timings.PredictedPerSecond > 0 {
					stats.TokensPerSecond = ev.Timings.PredictedPerSecond
				}
			}
		}
		if errors.Is(err, io.EOF) {
			// A server that closes without [DONE] has still sent what it had.
			return finish(stats, pieces), nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return stats, ctx.Err()
			}
			return stats, fmt.Errorf("the answer was cut off: %w", err)
		}
	}
}

func finish(stats Stats, pieces int) Stats {
	if stats.CompletionTokens == 0 {
		stats.CompletionTokens = pieces // a piece is about a token
	}
	return stats
}

// refusal is the error a server answered with: {"error": {"message": "..."}}, or {"error": "..."}.
func refusal(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	var e struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	text := ""
	if json.Unmarshal(body, &e) == nil {
		text = errorText(e.Error)
		if text == "" {
			text = e.Message
		}
	}
	// Some services answer with a list holding the error.
	if text == "" {
		var list []struct {
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(body, &list) == nil && len(list) > 0 {
			text = errorText(list[0].Error)
		}
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return withDetail("the service did not accept the API key", text)
	case resp.StatusCode == http.StatusTooManyRequests:
		return withDetail("the service refused for now: too many requests, or the quota is used up", text)
	case text == "":
		return fmt.Errorf("the model answered HTTP %d", resp.StatusCode)
	}
	return errors.New(text)
}

func withDetail(summary, detail string) error {
	if detail == "" {
		return errors.New(summary)
	}
	return fmt.Errorf("%s: %s", summary, detail)
}

// errorText reads the message of an "error" value, which is a string or an object with a message.
func errorText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return o.Message
	}
	return ""
}
