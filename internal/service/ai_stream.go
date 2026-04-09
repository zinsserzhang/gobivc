package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zinsserzhang/gobivc/internal/model"
)

// StreamCallback is called with each chunk of generated text.
type StreamCallback func(chunk string)

// StreamGenerator supports streaming text generation.
type StreamGenerator interface {
	GenerateStream(ctx context.Context, config model.ReportConfig, callback StreamCallback) (string, error)
}

// claudeStreamRequest extends the request with stream flag.
type claudeStreamRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system"`
	Messages  []claudeMessage `json:"messages"`
	Stream    bool            `json:"stream"`
}

// GenerateStream generates content using the Claude API with streaming.
func (g *ClaudeGenerator) GenerateStream(ctx context.Context, config model.ReportConfig, callback StreamCallback) (string, error) {
	systemPrompt := buildSystemPrompt()
	userPrompt := buildUserPrompt(config)
	maxTokens := getMaxTokens(config.Depth)

	reqBody := claudeStreamRequest{
		Model:     g.Model,
		MaxTokens: maxTokens,
		System:    systemPrompt,
		Messages: []claudeMessage{
			{Role: "user", Content: userPrompt},
		},
		Stream: true,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", g.BaseURL+"/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", g.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	return parseSSEStream(resp.Body, callback)
}

// parseSSEStream reads the SSE stream from Claude API and calls callback with text deltas.
func parseSSEStream(reader io.Reader, callback StreamCallback) (string, error) {
	var fullContent strings.Builder
	scanner := bufio.NewScanner(reader)
	// Increase buffer size for large events
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			fullContent.WriteString(event.Delta.Text)
			if callback != nil {
				callback(event.Delta.Text)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fullContent.String(), fmt.Errorf("stream read error: %w", err)
	}

	return fullContent.String(), nil
}
