package service

import (
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

// AIGenerator defines the interface for AI-powered report generation.
type AIGenerator interface {
	Generate(ctx context.Context, config model.ReportConfig) (string, error)
}

// ClaudeGenerator uses the Anthropic Claude API to generate reports.
type ClaudeGenerator struct {
	APIKey  string
	Model   string
	BaseURL string
	Client  *http.Client
}

// NewClaudeGenerator creates a new ClaudeGenerator with the given API key.
func NewClaudeGenerator(apiKey, modelName, baseURL string) *ClaudeGenerator {
	if modelName == "" {
		modelName = "claude-sonnet-4-20250514"
	}
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &ClaudeGenerator{
		APIKey:  apiKey,
		Model:   modelName,
		BaseURL: baseURL,
		Client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system"`
	Messages  []claudeMessage `json:"messages"`
}

type claudeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeResponse struct {
	Content []claudeContentBlock `json:"content"`
	Error   *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (g *ClaudeGenerator) Generate(ctx context.Context, config model.ReportConfig) (string, error) {
	systemPrompt := buildSystemPrompt()
	userPrompt := buildUserPrompt(config)
	maxTokens := getMaxTokens(config.Depth)

	reqBody := claudeRequest{
		Model:     g.Model,
		MaxTokens: maxTokens,
		System:    systemPrompt,
		Messages: []claudeMessage{
			{Role: "user", Content: userPrompt},
		},
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

	var claudeResp claudeResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if claudeResp.Error != nil {
		return "", fmt.Errorf("API error: %s", claudeResp.Error.Message)
	}

	var result strings.Builder
	for _, block := range claudeResp.Content {
		if block.Type == "text" {
			result.WriteString(block.Text)
		}
	}

	return result.String(), nil
}

func buildSystemPrompt() string {
	return `你是一位顶级的风险投资行业研究分析师，拥有丰富的行业研究和投资分析经验。你需要生成专业、严谨、有深度的行业研究报告。

报告要求：
1. 使用Markdown格式输出
2. 数据和论点要有逻辑支撑
3. 分析要客观、全面、有前瞻性
4. 语言专业但易于理解
5. 包含对投资机会和风险的分析
6. 使用中文撰写

报告结构应包含以下核心章节（根据深度要求可调整详略）：
- 摘要
- 行业概述与定义
- 市场规模与增长趋势
- 产业链分析
- 竞争格局
- 主要企业分析
- 技术发展趋势
- 政策与监管环境
- 投资机会与建议
- 风险提示
- 总结与展望`
}

func buildUserPrompt(config model.ReportConfig) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("请为我撰写一份关于「%s」的行业研究报告。\n\n", config.Topic))

	if config.Direction != "" {
		sb.WriteString(fmt.Sprintf("重点研究方向：%s\n\n", config.Direction))
	}

	switch config.Depth {
	case model.DepthBrief:
		sb.WriteString("报告深度：概览级别。请提供简洁的行业概览，重点突出关键数据和核心结论，篇幅控制在2000字左右。\n")
	case model.DepthStandard:
		sb.WriteString("报告深度：标准分析。请提供全面的行业分析，涵盖市场规模、竞争格局、技术趋势和投资建议，篇幅控制在5000字左右。\n")
	case model.DepthDeep:
		sb.WriteString("报告深度：深度研究。请提供详尽的深度研究报告，包含详细的数据分析、产业链拆解、企业对比、投资逻辑推演，篇幅控制在10000字左右。\n")
	}

	if config.CustomNotes != "" {
		sb.WriteString(fmt.Sprintf("\n用户特别要求：%s\n", config.CustomNotes))
	}

	sb.WriteString("\n请直接输出Markdown格式的报告内容，以一级标题开始。")

	return sb.String()
}

func getMaxTokens(depth model.ReportDepth) int {
	switch depth {
	case model.DepthBrief:
		return 4096
	case model.DepthDeep:
		return 16384
	default:
		return 8192
	}
}
