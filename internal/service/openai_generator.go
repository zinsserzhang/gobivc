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

// OpenAIGenerator uses OpenAI-compatible APIs (MiniMax, DeepSeek, etc.) to generate reports.
type OpenAIGenerator struct {
	APIKey  string
	Model   string
	BaseURL string // e.g. https://api.minimax.chat/v1
	Client  *http.Client
}

// NewOpenAIGenerator creates a new OpenAI-compatible generator.
func NewOpenAIGenerator(apiKey, modelName, baseURL string) *OpenAIGenerator {
	return &OpenAIGenerator{
		APIKey:  apiKey,
		Model:   modelName,
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		Client: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}
}

// -- OpenAI request/response types --

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type openaiChoice struct {
	Index   int `json:"index"`
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

type openaiResponse struct {
	Choices []openaiChoice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Generate implements AIGenerator for non-streaming generation.
func (g *OpenAIGenerator) Generate(ctx context.Context, config model.ReportConfig) (string, error) {
	messages := buildOpenAIMessages(config)
	maxTokens := getMaxTokens(config.Depth)

	reqBody := openaiRequest{
		Model:       g.Model,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Temperature: 0.7,
		Stream:      false,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", g.BaseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.APIKey)

	resp, err := g.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result openaiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if result.Error != nil {
		return "", fmt.Errorf("API error: %s", result.Error.Message)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("API returned no choices")
	}

	return result.Choices[0].Message.Content, nil
}

// GenerateStream implements StreamGenerator for SSE streaming.
func (g *OpenAIGenerator) GenerateStream(ctx context.Context, config model.ReportConfig, callback StreamCallback) (string, error) {
	messages := buildOpenAIMessages(config)
	maxTokens := getMaxTokens(config.Depth)

	reqBody := openaiRequest{
		Model:       g.Model,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Temperature: 0.7,
		Stream:      true,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", g.BaseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.APIKey)

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

	return parseOpenAISSEStream(resp.Body, callback)
}

// parseOpenAISSEStream reads OpenAI-format SSE stream.
func parseOpenAISSEStream(reader io.Reader, callback StreamCallback) (string, error) {
	var fullContent strings.Builder
	scanner := bufio.NewScanner(reader)
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
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if len(event.Choices) > 0 && event.Choices[0].Delta.Content != "" {
			text := event.Choices[0].Delta.Content
			fullContent.WriteString(text)
			if callback != nil {
				callback(text)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fullContent.String(), fmt.Errorf("stream read error: %w", err)
	}

	return fullContent.String(), nil
}

// buildOpenAIMessages constructs the messages array for the OpenAI-compatible API.
func buildOpenAIMessages(config model.ReportConfig) []openaiMessage {
	systemPrompt := buildSystemPrompt()
	userPrompt := buildUserPrompt(config)
	return []openaiMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
}
