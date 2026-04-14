package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/zinsserzhang/gobivc/internal/model"
	"github.com/zinsserzhang/gobivc/internal/qveris"
)

// compsTools defines the tools available to the Comps agent.
func buildCompsTools() []openaiToolDef {
	// Tool: fetch_stock_data
	fetchTool := openaiToolDef{Type: "function"}
	fetchTool.Function.Name = "fetch_stock_data"
	fetchTool.Function.Description = "从 Qveris.ai 实时拉取指定股票的市场数据，包括市值、P/E、P/S、P/B、EV/EBITDA、营收、毛利率等。必须传入市场和股票代码。"
	fetchTool.Function.Parameters = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"market": map[string]any{
				"type":        "string",
				"enum":        []string{"A股", "港股", "美股"},
				"description": "股票所属市场",
			},
			"symbol": map[string]any{
				"type":        "string",
				"description": "股票代码。A股格式: 600519.SH 或 000858.SZ；港股格式: 09988.HK；美股格式: AAPL",
			},
		},
		"required": []string{"market", "symbol"},
	}

	return []openaiToolDef{fetchTool}
}

// CompsAgentInput holds the input for the Comps agent.
type CompsAgentInput struct {
	Topic       string
	ProjectInfo string // formatted project info
	BPContent   string // uploaded BP content
}

// generateCompsReport runs the agent loop to generate a Comps report.
func (s *ReportService) generateCompsReport(ctx context.Context, reportID string, config model.ReportConfig) (string, error) {
	if s.qveris == nil || !s.qveris.IsConfigured() {
		return "", fmt.Errorf("Qveris 未配置，无法生成 Comps 报告")
	}

	rawGen, ok := s.generator.(*OpenAIGenerator)
	if !ok {
		return "", fmt.Errorf("当前 AI 模型不支持 Function Calling，请使用 OpenAI 兼容接口")
	}

	systemPrompt := `你是顶级的二级市场投研专家，擅长可比公司分析。

任务流程：
1. 阅读用户提供的项目信息和 BP 内容，识别行业和细分赛道
2. 针对 A股、港股、美股三个市场，分别确定 3-5 家最相关的可比上市公司
3. 对每家可比公司，调用 fetch_stock_data 工具获取实时市场数据
4. 基于返回的真实数据，生成完整的 Markdown 格式 Comps 分析报告

⚠️ 关键规则：
- 所有估值数据（市值、P/E、P/S、EV/EBITDA 等）必须通过 fetch_stock_data 工具获取
- 绝对禁止使用你训练数据中的过时股价或估值数据
- 如果某只股票调用失败或返回"数据不可用"，在报告中如实标注，不要编造数据
- 股票代码格式：A股 XXXXXX.SH/SZ，港股 XXXXX.HK，美股 TICKER

最终输出要求（在所有工具调用完成后输出）：

# [项目名称] 二级市场 Comps 分析

## 一、赛道识别
## 二、A股可比公司分析（含清单、估值倍数对比、Multiples 统计、特征解读）
## 三、港股可比公司分析
## 四、美股可比公司分析
## 五、跨市场 Multiples 对比
## 六、估值建议（含一级市场折价）
## 七、总结

在估值对比环节使用图表（用 ` + "```chart" + ` 代码块，格式见下）：
` + "```chart" + `
{"type":"bar","title":"A股 P/E 对比","labels":["公司A","公司B"],"datasets":[{"label":"P/E","data":[25.3,32.1]}]}
` + "```"

	// Build user prompt with project info + BP content
	var userPrompt strings.Builder
	userPrompt.WriteString(fmt.Sprintf("请为「%s」项目生成二级市场 Comps 分析报告。\n\n", config.Topic))

	if p := config.ProjectInfo; p != nil {
		userPrompt.WriteString("## 项目信息\n")
		if p.Industry != "" {
			userPrompt.WriteString(fmt.Sprintf("- 行业: %s\n", p.Industry))
		}
		if p.CoreProduct != "" {
			userPrompt.WriteString(fmt.Sprintf("- 核心产品: %s\n", p.CoreProduct))
		}
		if p.CompanyName != "" {
			userPrompt.WriteString(fmt.Sprintf("- 公司: %s\n", p.CompanyName))
		}
		userPrompt.WriteString("\n")
	}

	if len(config.Files) > 0 {
		userPrompt.WriteString("## BP / 项目材料\n\n")
		for _, f := range config.Files {
			if f.Text == "" {
				continue
			}
			text := f.Text
			if len([]rune(text)) > 4000 {
				text = string([]rune(text)[:4000])
			}
			userPrompt.WriteString(fmt.Sprintf("### %s\n%s\n\n", f.Name, text))
		}
	}

	userPrompt.WriteString("\n请开始分析并调用 fetch_stock_data 工具获取真实市场数据。")

	tools := buildCompsTools()

	// Tool handler: dispatches fetch_stock_data calls to Qveris
	handler := func(ctx context.Context, name, args string) (string, error) {
		switch name {
		case "fetch_stock_data":
			return s.handleFetchStockData(ctx, args)
		default:
			return "", fmt.Errorf("未知工具: %s", name)
		}
	}

	// Progress callback broadcasts to SSE subscribers
	progress := func(msg string) {
		log.Printf("INFO: comps agent: %s", msg)
		s.broadcast(reportID, fmt.Sprintf("\n> %s\n", msg))
	}

	return rawGen.ChatWithTools(ctx, systemPrompt, userPrompt.String(), tools, handler, 15, progress)
}

// handleFetchStockData implements the fetch_stock_data tool.
func (s *ReportService) handleFetchStockData(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Market string `json:"market"`
		Symbol string `json:"symbol"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	log.Printf("INFO: fetch_stock_data called: market=%s symbol=%s", args.Market, args.Symbol)

	if args.Symbol == "" {
		return "", fmt.Errorf("symbol 不能为空")
	}

	market := args.Market
	if market == "" {
		market = "美股"
	}

	data, err := s.qveris.FetchCompsDataByMarket(ctx, []string{args.Symbol}, market)
	if err != nil {
		return fmt.Sprintf(`{"error": "数据获取失败: %s", "symbol": "%s"}`, err.Error(), args.Symbol), nil
	}
	if len(data) == 0 {
		return fmt.Sprintf(`{"symbol": "%s", "status": "no_data"}`, args.Symbol), nil
	}

	// Return metrics as JSON
	result, _ := json.Marshal(compsToResponse(data[0]))
	return string(result), nil
}

func compsToResponse(m qveris.CompanyMetrics) map[string]any {
	return map[string]any{
		"symbol":         m.Symbol,
		"name":           m.Name,
		"market_cap":     m.MarketCap,
		"price":          m.Price,
		"pe_ratio":       m.PE,
		"ps_ratio":       m.PS,
		"pb_ratio":       m.PB,
		"ev_ebitda":      m.EVEBITDA,
		"revenue":        m.Revenue,
		"net_income":     m.NetIncome,
		"gross_margin":   m.GrossMargin,
		"revenue_growth": m.RevenueGrowth,
		"source":         m.Source,
	}
}
