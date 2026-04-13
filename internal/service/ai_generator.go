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
	systemPrompt := buildSystemPrompt(config)
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

func buildSystemPrompt(config model.ReportConfig) string {
	switch config.ReportType {
	case model.TypeDDChecklist:
		return `你是一位顶级风险投资机构的投资总监，拥有丰富的投资尽职调查经验。你需要根据提供的项目材料（BP、Datapack等），生成一份专业的投资尽调清单。

要求：
1. 使用Markdown格式输出
2. 基于提供的项目材料内容，针对性地生成尽调要点
3. 涵盖尽调的各个维度，标注优先级和关键风险点
4. 使用中文撰写

尽调清单应涵盖以下维度（根据项目类型可调整）：
- 公司基本情况核实
- 业务模式与商业逻辑验证
- 财务数据核实与分析
- 技术/产品尽调
- 市场与竞争尽调
- 团队背景调查
- 法律与合规尽调
- 知识产权尽调
- 客户与供应商访谈清单
- 关键风险点与红旗事项（Red Flags）
- 估值合理性分析
- 交易结构建议

每个维度下列出具体的尽调事项、需要获取的文件/数据、访谈对象、以及该项的风险等级（高/中/低）。`

	case model.TypeInvestmentMemo:
		return `你是一位顶级风险投资机构的投资经理，擅长撰写投资备忘录和立项材料。你需要根据提供的项目材料（BP、Datapack等），生成一份可供投委会审议的投资备忘录。

要求：
1. 使用Markdown格式输出
2. 基于提供的项目材料，提炼关键信息并加入专业分析
3. 逻辑严谨、数据详实、结论清晰
4. 使用中文撰写

投资备忘录应包含以下章节：
- 项目概览（一页纸摘要）
- 投资亮点（Investment Highlights）
- 公司介绍与发展历程
- 行业分析与市场机会
- 商业模式分析
- 产品/技术分析
- 竞争格局与竞争优势
- 财务分析与预测
- 团队评估
- 估值分析与交易条款
- 投资逻辑与论点（Investment Thesis）
- 主要风险与缓释措施
- 退出路径分析
- 投资建议与结论`

	case model.TypeQuestions:
		return `你是一位顶级风险投资机构的合伙人，擅长从投资决策角度提炼核心问题。你需要根据提供的项目材料，梳理出投资该项目时需要重点关注和验证的核心问题。

要求：
1. 使用Markdown格式输出
2. 基于提供的项目材料，发现潜在问题和需要验证的假设
3. 每个问题说明为什么重要、如何验证、潜在风险
4. 区分优先级（关键/重要/一般）
5. 使用中文撰写

核心问题应涵盖以下方面：
- 商业模式核心假设验证
- 市场规模和增长逻辑的关键问题
- 技术/产品的核心风险点
- 财务数据中的异常和疑问
- 团队能力和稳定性
- 竞争壁垒的可持续性
- 客户获取和留存的关键问题
- 监管和合规风险
- 估值合理性
- 退出路径可行性

对每个问题，请给出：问题描述、重要性说明、建议的验证方式、风险等级。`

	default:
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
}

func buildUserPrompt(config model.ReportConfig) string {
	var sb strings.Builder

	switch config.ReportType {
	case model.TypeDDChecklist:
		sb.WriteString(fmt.Sprintf("请为「%s」项目生成一份投资尽职调查清单。\n\n", config.Topic))
	case model.TypeInvestmentMemo:
		sb.WriteString(fmt.Sprintf("请为「%s」项目撰写一份投资备忘录（立项材料）。\n\n", config.Topic))
	case model.TypeQuestions:
		sb.WriteString(fmt.Sprintf("请为「%s」项目梳理投资决策的核心问题关注清单。\n\n", config.Topic))
	default:
		sb.WriteString(fmt.Sprintf("请为我撰写一份关于「%s」的行业研究报告。\n\n", config.Topic))
	}

	if config.Direction != "" {
		sb.WriteString(fmt.Sprintf("重点方向：%s\n\n", config.Direction))
	}

	switch config.Depth {
	case model.DepthBrief:
		sb.WriteString("深度：概览级别，重点突出关键要点，篇幅精简。\n")
	case model.DepthStandard:
		sb.WriteString("深度：标准分析，涵盖各核心维度。\n")
	case model.DepthDeep:
		sb.WriteString("深度：详尽深度分析，尽可能全面详细。\n")
	}

	// Attach uploaded file contents
	if len(config.Files) > 0 {
		sb.WriteString("\n\n===== 以下是项目提供的材料，请基于这些材料进行分析 =====\n\n")
		for i, f := range config.Files {
			sb.WriteString(fmt.Sprintf("--- 材料 %d: %s ---\n", i+1, f.Name))
			if f.Text != "" {
				sb.WriteString(f.Text)
			} else {
				sb.WriteString("（该文件未能提取文本内容）")
			}
			sb.WriteString("\n\n")
		}
		sb.WriteString("===== 项目材料结束 =====\n\n")
	}

	if config.CustomNotes != "" {
		sb.WriteString(fmt.Sprintf("\n用户特别要求：%s\n", config.CustomNotes))
	}

	sb.WriteString("\n请直接输出Markdown格式的内容，以一级标题开始。")

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
