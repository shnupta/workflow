package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	claudeAPIBase    = "https://api.anthropic.com/v1"
	claudeAPIModel   = "claude-sonnet-4-5"
	claudeAPIVersion = "2023-06-01"
	maxTokens        = 8192
)

// ClaudeAPI implements Runner using the Anthropic Messages API with streaming.
// Falls back gracefully if the API key is missing (returns an error on Run).
type ClaudeAPI struct {
	// APIKey is the Anthropic API key. Required.
	APIKey string

	// Model overrides the default model (claude-sonnet-4-5).
	Model string

	// SystemPrompt is prepended to every session.
	SystemPrompt string

	client *http.Client
}

func (c *ClaudeAPI) Name() string { return "claude_api" }

func (c *ClaudeAPI) model() string {
	if c.Model != "" {
		return c.Model
	}
	return claudeAPIModel
}

func (c *ClaudeAPI) httpClient() *http.Client {
	if c.client != nil {
		return c.client
	}
	c.client = &http.Client{Timeout: 5 * time.Minute}
	return c.client
}

// Validate checks the API key is set.
func (c *ClaudeAPI) Validate() error {
	if c.APIKey == "" {
		return fmt.Errorf("claude_api: no API key set (add anthropic_api_key to workflow.json)")
	}
	return nil
}

// Run streams a Claude API response, converting SSE events to normalised Events.
// Note: ClaudeAPI does not support tool use or session resumption — it's a simple
// single-turn text runner suitable as a fallback when the claude CLI is unavailable.
func (c *ClaudeAPI) Run(ctx context.Context, opts RunOptions) (<-chan Event, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	messages := []map[string]string{
		{"role": "user", "content": opts.Prompt},
	}

	payload := map[string]any{
		"model":      c.model(),
		"max_tokens": maxTokens,
		"stream":     true,
		"messages":   messages,
	}
	if c.SystemPrompt != "" {
		payload["system"] = c.SystemPrompt
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("claude_api: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", claudeAPIBase+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("claude_api: build request: %w", err)
	}
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("anthropic-version", claudeAPIVersion)
	req.Header.Set("content-type", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("claude_api: request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("claude_api: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		var textBuf strings.Builder
		scanner := bufio.NewScanner(resp.Body)

		for scanner.Scan() {
			if ctx.Err() != nil {
				return
			}
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var evt map[string]json.RawMessage
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				log.Printf("claude_api: unparseable SSE: %s", data)
				continue
			}

			evtType := strings.Trim(string(evt["type"]), `"`)
			switch evtType {
			case "content_block_delta":
				var delta struct {
					Delta struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"delta"`
				}
				if err := json.Unmarshal([]byte(data), &delta); err == nil {
					if delta.Delta.Type == "text_delta" {
						textBuf.WriteString(delta.Delta.Text)
						select {
						case ch <- Event{Kind: EventText, Content: delta.Delta.Text}:
						case <-ctx.Done():
							return
						}
					}
				}
			case "message_stop":
				select {
				case ch <- Event{Kind: EventDone, ExitCode: 0}:
				case <-ctx.Done():
				}
				return
			case "error":
				var apiErr struct {
					Error struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				_ = json.Unmarshal([]byte(data), &apiErr)
				msg := apiErr.Error.Message
				if msg == "" {
					msg = "unknown API error"
				}
				select {
				case ch <- Event{Kind: EventError, Err: fmt.Errorf("claude_api: %s", msg)}:
				case <-ctx.Done():
				}
				return
			}
		}

		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			select {
			case ch <- Event{Kind: EventError, Err: fmt.Errorf("claude_api: scanner: %w", err)}:
			default:
			}
		}
	}()

	return ch, nil
}
