package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/zinsserzhang/gobivc/internal/model"
	"github.com/zinsserzhang/gobivc/internal/qveris"
)

// PeerSuggestion is an AI-suggested peer company.
type PeerSuggestion struct {
	Market  string `json:"market"`  // "A股", "港股", "美股"
	Symbol  string `json:"symbol"`  // stock symbol
	Name    string `json:"name"`    // company name
	Reason  string `json:"reason"`  // why it's a peer
}

// PeerDiscovery holds the result of AI peer discovery from BP.
type PeerDiscovery struct {
	Industry    string           `json:"industry"`
	SubSector   string           `json:"sub_sector"`
	APeers      []PeerSuggestion `json:"a_peers"`
	HKPeers     []PeerSuggestion `json:"hk_peers"`
	USPeers     []PeerSuggestion `json:"us_peers"`
}

// discoverPeers uses AI to analyze BP and extract peer companies from each market.
func (s *ReportService) discoverPeers(ctx context.Context, config model.ReportConfig) (*PeerDiscovery, error) {
	systemPrompt := `你是一位资深的二级市场研究员，擅长寻找同赛道可比公司。

任务：基于用户提供的项目材料（BP、Datapack等），识别项目所在的细分赛道，并分别找出 A股、港股、美股市场上最相关的 3-5 家可比上市公司。

输出要求：
1. **必须严格输出 JSON 格式**，不要任何其他文字、解释、markdown标记
2. 股票代码格式：
   - A股：数字代码.交易所后缀（如 "600519.SH" 或 "000858.SZ"）
   - 港股：数字代码.HK（如 "09988.HK" 或 "0700.HK"）
   - 美股：标准ticker（如 "AAPL", "NVDA"）
3. 选择可比公司的标准：业务模式相似、产品/服务相似、客户群体相似、行业赛道相同

输出 JSON 格式（严格遵守）：
{
  "industry": "所属行业（如：人工智能）",
  "sub_sector": "细分赛道（如：大模型基础设施）",
  "a_peers": [
    {"market": "A股", "symbol": "600519.SH", "name": "公司名称", "reason": "可比原因"}
  ],
  "hk_peers": [
    {"market": "港股", "symbol": "09988.HK", "name": "阿里巴巴", "reason": "可比原因"}
  ],
  "us_peers": [
    {"market": "美股", "symbol": "NVDA", "name": "英伟达", "reason": "可比原因"}
  ]
}`

	var userPrompt strings.Builder
	userPrompt.WriteString(fmt.Sprintf("请为「%s」识别同赛道的二级市场上市可比公司。\n\n", config.Topic))

	if p := config.ProjectInfo; p != nil {
		if p.Industry != "" {
			userPrompt.WriteString(fmt.Sprintf("行业：%s\n", p.Industry))
		}
		if p.CoreProduct != "" {
			userPrompt.WriteString(fmt.Sprintf("核心产品：%s\n", p.CoreProduct))
		}
	}

	if len(config.Files) > 0 {
		userPrompt.WriteString("\n项目材料：\n")
		for _, f := range config.Files {
			if f.Text != "" {
				// Use first 3000 chars per file to keep context manageable
				text := f.Text
				if len([]rune(text)) > 3000 {
					text = string([]rune(text)[:3000])
				}
				userPrompt.WriteString(fmt.Sprintf("--- %s ---\n%s\n\n", f.Name, text))
			}
		}
	}

	userPrompt.WriteString("\n请输出 JSON 格式的可比公司分析结果。")

	// Build a minimal config for the discovery call
	discoveryConfig := config
	discoveryConfig.CustomNotes = userPrompt.String()

	// Use non-streaming generator for structured output
	// Call generator directly with custom prompts
	content, err := s.callAIRaw(ctx, systemPrompt, userPrompt.String())
	if err != nil {
		return nil, fmt.Errorf("peer discovery failed: %w", err)
	}

	// Strip markdown code fence if present
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	// Extract JSON
	if i := strings.Index(content, "{"); i > 0 {
		content = content[i:]
	}
	if i := strings.LastIndex(content, "}"); i > 0 && i < len(content)-1 {
		content = content[:i+1]
	}

	var result PeerDiscovery
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("parse peer discovery JSON failed: %w, content: %s", err, content)
	}

	log.Printf("INFO: peer discovery: industry=%s, A=%d HK=%d US=%d",
		result.Industry, len(result.APeers), len(result.HKPeers), len(result.USPeers))

	return &result, nil
}

// callAIRaw invokes the AI with raw system+user prompts.
func (s *ReportService) callAIRaw(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	// Use a stub config to drive generation - generator uses config for prompts
	// but we'll override by using a custom topic
	stubConfig := model.ReportConfig{
		Topic:       "peer_discovery",
		CustomNotes: systemPrompt + "\n\n" + userPrompt,
		Depth:       model.DepthBrief, // short output
	}

	// Check if the generator supports raw prompts
	if raw, ok := s.generator.(RawGenerator); ok {
		return raw.GenerateRaw(ctx, systemPrompt, userPrompt, 4096)
	}

	// Fallback: use regular Generate (won't be as clean)
	return s.generator.Generate(ctx, stubConfig)
}

// fetchAllCompsData fetches Qveris data for all peer companies.
func (s *ReportService) fetchAllCompsData(ctx context.Context, discovery *PeerDiscovery) (map[string][]qveris.CompanyMetrics, error) {
	if s.qveris == nil || !s.qveris.IsConfigured() {
		return nil, fmt.Errorf("Qveris 未配置，跳过实时市场数据")
	}

	result := make(map[string][]qveris.CompanyMetrics)

	if syms := extractSymbols(discovery.APeers); len(syms) > 0 {
		if data, err := s.qveris.FetchCompsDataByMarket(ctx, syms, "A股"); err == nil {
			result["A股"] = data
		} else {
			log.Printf("WARNING: qveris A股 fetch failed: %v", err)
		}
	}
	if syms := extractSymbols(discovery.HKPeers); len(syms) > 0 {
		if data, err := s.qveris.FetchCompsDataByMarket(ctx, syms, "港股"); err == nil {
			result["港股"] = data
		} else {
			log.Printf("WARNING: qveris 港股 fetch failed: %v", err)
		}
	}
	if syms := extractSymbols(discovery.USPeers); len(syms) > 0 {
		if data, err := s.qveris.FetchCompsDataByMarket(ctx, syms, "美股"); err == nil {
			result["美股"] = data
		} else {
			log.Printf("WARNING: qveris 美股 fetch failed: %v", err)
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("未能从 Qveris 获取任何市场数据")
	}

	return result, nil
}

func extractSymbols(peers []PeerSuggestion) []string {
	result := make([]string, 0, len(peers))
	for _, p := range peers {
		result = append(result, p.Symbol)
	}
	return result
}

// formatCompsContext formats the discovered peers + market data for injection into the final AI prompt.
func formatCompsContext(discovery *PeerDiscovery, marketData map[string][]qveris.CompanyMetrics) string {
	var sb strings.Builder

	sb.WriteString("⚠️ 以下是从 Qveris.ai 实时 API 拉取的最新二级市场数据。**报告中使用的所有估值数据必须引用以下表格，禁止使用模型训练时的旧数据。**\n\n")
	sb.WriteString(fmt.Sprintf("数据拉取时间：%s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	sb.WriteString(fmt.Sprintf("## 赛道识别\n- 行业：%s\n- 细分赛道：%s\n\n", discovery.Industry, discovery.SubSector))

	writePeerGroup(&sb, "A股可比公司", discovery.APeers, marketData["A股"])
	writePeerGroup(&sb, "港股可比公司", discovery.HKPeers, marketData["港股"])
	writePeerGroup(&sb, "美股可比公司", discovery.USPeers, marketData["美股"])

	return sb.String()
}

func writePeerGroup(sb *strings.Builder, title string, peers []PeerSuggestion, data []qveris.CompanyMetrics) {
	if len(peers) == 0 {
		return
	}

	sb.WriteString(fmt.Sprintf("\n## %s\n\n", title))

	// Peer reasons
	sb.WriteString("### 可比公司清单\n\n")
	for _, p := range peers {
		sb.WriteString(fmt.Sprintf("- **%s (%s)**: %s\n", p.Name, p.Symbol, p.Reason))
	}

	// Market data table if available
	if len(data) > 0 {
		sb.WriteString("\n### 市场数据 (来自 Qveris.ai)\n\n")
		sb.WriteString("| 公司 | 代码 | 市值 | P/E | P/S | P/B | EV/EBITDA | 营收 | 毛利率 | 营收增速 |\n")
		sb.WriteString("|------|------|------|-----|-----|-----|-----------|------|--------|----------|\n")
		for _, m := range data {
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
				m.Name, m.Symbol, m.MarketCap, m.PE, m.PS, m.PB, m.EVEBITDA, m.Revenue, m.GrossMargin, m.RevenueGrowth))
		}
	}
	sb.WriteString("\n")
}
