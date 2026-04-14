package service

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

	"github.com/zinsserzhang/gobivc/internal/model"
)

// streamRequestWithRetry executes a streaming request with retry on transient errors.
func streamRequestWithRetry(client *http.Client, req *http.Request, body []byte) (*http.Response, error) {
	maxRetries := 4
	backoff := 2 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if body != nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
		}

		resp, err := client.Do(req)
		if err != nil {
			if attempt == maxRetries {
				return nil, err
			}
			log.Printf("WARNING: stream request failed (attempt %d/%d): %v, retrying in %v", attempt+1, maxRetries, err, backoff)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		if resp.StatusCode == 429 || resp.StatusCode == 529 || (resp.StatusCode >= 500 && resp.StatusCode < 600) {
			if attempt == maxRetries {
				return resp, nil
			}
			resp.Body.Close()
			log.Printf("WARNING: stream API returned %d (attempt %d/%d), retrying in %v", resp.StatusCode, attempt+1, maxRetries, backoff)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		return resp, nil
	}
	return nil, fmt.Errorf("max retries exceeded")
}

// doRequestWithRetry executes an HTTP request with retry on transient errors.
func (g *OpenAIGenerator) doRequestWithRetry(req *http.Request, body []byte) (*http.Response, error) {
	maxRetries := 4
	backoff := 2 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Clone request body for each retry
		if body != nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
		}

		resp, err := g.Client.Do(req)
		if err != nil {
			if attempt == maxRetries {
				return nil, err
			}
			log.Printf("WARNING: request failed (attempt %d/%d): %v, retrying in %v", attempt+1, maxRetries, err, backoff)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		// Retry on 429 (rate limit), 5xx (server errors), 529 (overloaded)
		if resp.StatusCode == 429 || resp.StatusCode == 529 || (resp.StatusCode >= 500 && resp.StatusCode < 600) {
			if attempt == maxRetries {
				return resp, nil // return the final response for error handling
			}
			resp.Body.Close()
			log.Printf("WARNING: API returned %d (attempt %d/%d), retrying in %v", resp.StatusCode, attempt+1, maxRetries, backoff)
			time.Sleep(backoff)
			backoff *= 2
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("max retries exceeded")
}

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
	Temperature float64         `json:"temperature"`
	TopP        float64         `json:"top_p,omitempty"`
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
		Temperature: 1.0,
		TopP:        0.95,
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

	resp, err := g.doRequestWithRetry(req, bodyBytes)
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

	content := result.Choices[0].Message.Content
	content = stripThinkingBlocks(content)
	return content, nil
}

// GenerateRaw performs a non-streaming AI call with custom system+user prompts.
func (g *OpenAIGenerator) GenerateRaw(ctx context.Context, systemPrompt, userPrompt string, maxTokens int) (string, error) {
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	messages := []openaiMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	reqBody := openaiRequest{
		Model:       g.Model,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Temperature: 0.3, // low temp for structured output
		TopP:        0.9,
		Stream:      false,
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", g.BaseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.APIKey)

	resp, err := g.doRequestWithRetry(req, bodyBytes)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result openaiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	if result.Error != nil {
		return "", fmt.Errorf("API error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return stripThinkingBlocks(result.Choices[0].Message.Content), nil
}

// stripThinkingBlocks removes <think>...</think> blocks from content.
func stripThinkingBlocks(s string) string {
	for {
		start := strings.Index(s, "<think>")
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], "</think>")
		if end == -1 {
			s = s[:start]
			break
		}
		s = s[:start] + s[start+end+len("</think>"):]
	}
	return strings.TrimSpace(s)
}

// GenerateStream implements StreamGenerator for SSE streaming.
func (g *OpenAIGenerator) GenerateStream(ctx context.Context, config model.ReportConfig, callback StreamCallback) (string, error) {
	messages := buildOpenAIMessages(config)
	maxTokens := getMaxTokens(config.Depth)

	reqBody := openaiRequest{
		Model:       g.Model,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Temperature: 1.0,
		TopP:        0.95,
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

	// Use retry-enabled client for streaming (only retries the initial connection)
	longTimeoutClient := &http.Client{Timeout: 15 * time.Minute}
	resp, err := streamRequestWithRetry(longTimeoutClient, req, bodyBytes)
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
// Filters out thinking/reasoning content (e.g. <think>...</think> blocks).
func parseOpenAISSEStream(reader io.Reader, callback StreamCallback) (string, error) {
	var fullContent strings.Builder
	var inThinking bool
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
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if len(event.Choices) == 0 {
			continue
		}

		// Skip reasoning_content field entirely
		text := event.Choices[0].Delta.Content
		if text == "" {
			continue
		}

		// Filter <think>...</think> blocks that some models embed in content
		for len(text) > 0 {
			if inThinking {
				endIdx := strings.Index(text, "</think>")
				if endIdx == -1 {
					break // entire chunk is thinking, skip
				}
				text = text[endIdx+len("</think>"):]
				inThinking = false
				continue
			}

			startIdx := strings.Index(text, "<think>")
			if startIdx == -1 {
				// No thinking tag, output the text
				fullContent.WriteString(text)
				if callback != nil {
					callback(text)
				}
				break
			}

			// Output text before <think>
			if startIdx > 0 {
				before := text[:startIdx]
				fullContent.WriteString(before)
				if callback != nil {
					callback(before)
				}
			}

			// Check if closing tag is in the same chunk
			endIdx := strings.Index(text[startIdx:], "</think>")
			if endIdx == -1 {
				inThinking = true
				break
			}
			text = text[startIdx+endIdx+len("</think>"):]
		}
	}

	if err := scanner.Err(); err != nil {
		return fullContent.String(), fmt.Errorf("stream read error: %w", err)
	}

	return fullContent.String(), nil
}

// buildOpenAIMessages constructs the messages array for the OpenAI-compatible API.
func buildOpenAIMessages(config model.ReportConfig) []openaiMessage {
	systemPrompt := buildSystemPrompt(config)
	userPrompt := buildUserPrompt(config)
	return []openaiMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
}
