package qveris

import (
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

// Client wraps the Qveris.ai API for financial data.
type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client

	// Cached tool IDs per query to avoid redundant searches
	toolCache map[string]cachedTool
}

type cachedTool struct {
	toolID   string
	searchID string
	expires  time.Time
}

// NewClient creates a new Qveris API client.
func NewClient(apiKey string) *Client {
	baseURL := "https://qveris.ai/api/v1"
	if strings.HasPrefix(apiKey, "sk-cn-") {
		baseURL = "https://qveris.cn/api/v1"
	}
	return &Client{
		APIKey:    apiKey,
		BaseURL:   baseURL,
		HTTP:      &http.Client{Timeout: 60 * time.Second},
		toolCache: make(map[string]cachedTool),
	}
}

func (c *Client) IsConfigured() bool { return c.APIKey != "" }

// -- Search for tools --

type searchResponse struct {
	SearchID string       `json:"search_id"`
	Total    int          `json:"total"`
	Tools    []searchTool `json:"tools"`
	Results  []searchTool `json:"results"` // Qveris actually uses "results"
}

// getTools returns the tool list, handling both schemas.
func (r *searchResponse) getTools() []searchTool {
	if len(r.Results) > 0 {
		return r.Results
	}
	return r.Tools
}

type searchTool struct {
	ToolID      string          `json:"tool_id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	SuccessRate float64         `json:"success_rate"`
	CreditCost  int             `json:"credit_cost"`
}

// SearchTools finds available tools matching a query.
func (c *Client) SearchTools(ctx context.Context, query string, limit int) (*searchResponse, error) {
	if limit <= 0 {
		limit = 10
	}
	body, _ := json.Marshal(map[string]any{
		"query": query,
		"limit": limit,
	})

	data, err := c.doRequest(ctx, "POST", "/search", body)
	if err != nil {
		return nil, err
	}

	log.Printf("DEBUG: qveris search response: %s", truncate(string(data), 500))

	var resp searchResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("qveris: parse search response: %w", err)
	}
	return &resp, nil
}

// -- Execute a tool --

type executeResponse struct {
	ExecutionID string          `json:"execution_id"`
	Result      json.RawMessage `json:"result"`
	Success     bool            `json:"success"`
	ErrorMsg    string          `json:"error_message"`
}

// ExecuteTool runs a discovered tool with parameters.
func (c *Client) ExecuteTool(ctx context.Context, toolID, searchID string, params map[string]any) (*executeResponse, error) {
	body, _ := json.Marshal(map[string]any{
		"search_id":         searchID,
		"parameters":        params,
		"max_response_size": 40960,
	})

	data, err := c.doRequest(ctx, "POST", "/tools/execute?tool_id="+toolID, body)
	if err != nil {
		return nil, err
	}

	log.Printf("DEBUG: qveris execute response (tool=%s): %s", toolID, truncate(string(data), 500))

	var resp executeResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("qveris: parse execute response: %w", err)
	}

	if !resp.Success && resp.ErrorMsg != "" {
		return nil, fmt.Errorf("qveris: tool execution failed: %s", resp.ErrorMsg)
	}

	return &resp, nil
}

// -- High-level: Fetch Comps Data --

// CompanyMetrics holds key financial metrics for a company.
type CompanyMetrics struct {
	Symbol        string `json:"symbol"`
	Name          string `json:"name"`
	MarketCap     string `json:"market_cap"`
	Price         string `json:"price"`
	PE            string `json:"pe_ratio"`
	PS            string `json:"ps_ratio"`
	PB            string `json:"pb_ratio"`
	EVEBITDA      string `json:"ev_ebitda"`
	Revenue       string `json:"revenue"`
	NetIncome     string `json:"net_income"`
	GrossMargin   string `json:"gross_margin"`
	RevenueGrowth string `json:"revenue_growth"`
	Source        string `json:"source"` // 数据源标识
}

// getFinancialTool searches for a suitable financial data tool, caches the result.
func (c *Client) getFinancialTool(ctx context.Context, market string) (toolID, searchID string, err error) {
	cacheKey := "financial-" + market
	if cached, ok := c.toolCache[cacheKey]; ok && time.Now().Before(cached.expires) {
		return cached.toolID, cached.searchID, nil
	}

	// Market-specific search queries
	var queries []string
	switch market {
	case "A股":
		queries = []string{
			"China A-share stock financial data P/E ratio",
			"Chinese stock market fundamentals",
			"stock quote fundamentals financial ratios",
		}
	case "港股":
		queries = []string{
			"Hong Kong stock financial data P/E ratio",
			"HK stock market fundamentals valuation",
			"stock quote fundamentals financial ratios",
		}
	default: // 美股
		queries = []string{
			"US stock financial data P/E ratio market cap",
			"stock fundamentals valuation metrics",
			"stock quote fundamentals financial ratios",
		}
	}

	for _, q := range queries {
		searchResp, serr := c.SearchTools(ctx, q, 10)
		if serr != nil {
			log.Printf("WARNING: qveris search failed for '%s': %v", q, serr)
			continue
		}
		tools := searchResp.getTools()
		if len(tools) == 0 {
			continue
		}

		// Pick the first tool (already ranked by relevance)
		tool := tools[0]
		log.Printf("INFO: qveris selected tool: %s (%s) for market %s",
			tool.Name, tool.ToolID, market)

		c.toolCache[cacheKey] = cachedTool{
			toolID:   tool.ToolID,
			searchID: searchResp.SearchID,
			expires:  time.Now().Add(10 * time.Minute),
		}
		return tool.ToolID, searchResp.SearchID, nil
	}

	return "", "", fmt.Errorf("qveris: no financial tool found for market %s", market)
}

// FetchCompsData fetches financial metrics for a list of company symbols.
func (c *Client) FetchCompsData(ctx context.Context, symbols []string) ([]CompanyMetrics, error) {
	return c.FetchCompsDataByMarket(ctx, symbols, "美股")
}

// FetchCompsDataByMarket fetches metrics with market-specific tool selection.
func (c *Client) FetchCompsDataByMarket(ctx context.Context, symbols []string, market string) ([]CompanyMetrics, error) {
	if !c.IsConfigured() {
		return nil, fmt.Errorf("qveris: API key not configured")
	}
	if len(symbols) == 0 {
		return nil, nil
	}

	log.Printf("INFO: qveris: fetching %s comps data for %d symbols", market, len(symbols))

	toolID, searchID, err := c.getFinancialTool(ctx, market)
	if err != nil {
		return nil, err
	}

	var results []CompanyMetrics
	for _, symbol := range symbols {
		symbol = strings.TrimSpace(symbol)
		if symbol == "" {
			continue
		}

		metrics, err := c.fetchSingleCompany(ctx, toolID, searchID, symbol)
		if err != nil {
			log.Printf("WARNING: qveris fetch '%s' failed: %v", symbol, err)
			results = append(results, CompanyMetrics{
				Symbol: symbol,
				Name:   symbol,
				Source: "获取失败",
			})
			continue
		}
		results = append(results, *metrics)
		// Small delay to avoid rate limiting
		time.Sleep(200 * time.Millisecond)
	}

	return results, nil
}

func (c *Client) fetchSingleCompany(ctx context.Context, toolID, searchID, symbol string) (*CompanyMetrics, error) {
	// Normalize symbol for US market (strip .US, etc)
	cleanSymbol := strings.TrimSpace(symbol)

	resp, err := c.ExecuteTool(ctx, toolID, searchID, map[string]any{
		"symbol": cleanSymbol,
		"ticker": cleanSymbol,
		"code":   cleanSymbol,
	})
	if err != nil {
		return nil, err
	}

	// Flatten result: try to parse as object, or unwrap nested data
	rawData := flattenResult(resp.Result)

	metrics := &CompanyMetrics{
		Symbol:        symbol,
		Name:          extractString(rawData, "name", "company_name", "shortName", "longName", "companyName"),
		MarketCap:     extractMoney(rawData, "market_cap", "marketCap", "mktCap", "market_capitalization"),
		Price:         extractString(rawData, "price", "current_price", "currentPrice", "regularMarketPrice", "last_price"),
		PE:            extractNumber(rawData, "pe_ratio", "pe", "trailingPE", "peRatio", "price_earnings_ratio"),
		PS:            extractNumber(rawData, "ps_ratio", "ps", "priceToSales", "psRatio", "price_to_sales"),
		PB:            extractNumber(rawData, "pb_ratio", "pb", "priceToBook", "pbRatio", "price_to_book"),
		EVEBITDA:      extractNumber(rawData, "ev_ebitda", "evEbitda", "enterpriseToEbitda", "ev_to_ebitda"),
		Revenue:       extractMoney(rawData, "revenue", "totalRevenue", "total_revenue", "revenueTtm"),
		NetIncome:     extractMoney(rawData, "net_income", "netIncome", "net_profit", "netIncomeTtm"),
		GrossMargin:   extractPercent(rawData, "gross_margin", "grossMargin", "grossMargins", "gross_profit_margin"),
		RevenueGrowth: extractPercent(rawData, "revenue_growth", "revenueGrowth", "revenue_growth_yoy"),
		Source:        "Qveris.ai",
	}

	if metrics.Name == "-" || metrics.Name == "" {
		metrics.Name = symbol
	}

	// Sanity check: if we didn't get ANY useful data, mark as failed
	if metrics.MarketCap == "-" && metrics.PE == "-" && metrics.Price == "-" && metrics.Revenue == "-" {
		log.Printf("WARNING: qveris returned no usable data for %s, raw: %s", symbol, truncate(string(resp.Result), 300))
		metrics.Source = "数据不可用"
	}

	return metrics, nil
}

// flattenResult tries to unwrap common nested response shapes.
func flattenResult(data json.RawMessage) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err == nil {
		// Common nesting: { "data": {...} } or { "result": {...} } or { "quote": {...} }
		for _, key := range []string{"data", "result", "quote", "summary", "defaultKeyStatistics", "financialData"} {
			if nested, ok := m[key]; ok {
				if nestedMap, ok := nested.(map[string]any); ok && len(nestedMap) > 0 {
					// Merge into parent
					for k, v := range nestedMap {
						m[k] = v
					}
				}
			}
		}
		return m
	}
	return map[string]any{}
}

// extractString tries multiple keys and returns the first non-empty value.
func extractString(data map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := data[k]; ok && v != nil {
			s := fmt.Sprintf("%v", v)
			if s != "" && s != "<nil>" && s != "0" {
				return s
			}
		}
	}
	return "-"
}

// extractNumber formats as a number with 2 decimals.
func extractNumber(data map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := data[k]; ok && v != nil {
			switch n := v.(type) {
			case float64:
				if n != 0 {
					return fmt.Sprintf("%.2f", n)
				}
			case int:
				if n != 0 {
					return fmt.Sprintf("%d", n)
				}
			case string:
				if n != "" && n != "0" {
					return n
				}
			}
		}
	}
	return "-"
}

// extractMoney formats large numbers in readable form (M/B/T).
func extractMoney(data map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := data[k]; ok && v != nil {
			var n float64
			switch val := v.(type) {
			case float64:
				n = val
			case int:
				n = float64(val)
			case string:
				if val == "" || val == "0" {
					continue
				}
				fmt.Sscanf(val, "%f", &n)
			}
			if n == 0 {
				continue
			}
			return formatMoney(n)
		}
	}
	return "-"
}

func formatMoney(n float64) string {
	abs := n
	if abs < 0 {
		abs = -n
	}
	switch {
	case abs >= 1e12:
		return fmt.Sprintf("%.2fT", n/1e12)
	case abs >= 1e9:
		return fmt.Sprintf("%.2fB", n/1e9)
	case abs >= 1e6:
		return fmt.Sprintf("%.2fM", n/1e6)
	case abs >= 1e4:
		return fmt.Sprintf("%.2fK", n/1e3)
	default:
		return fmt.Sprintf("%.2f", n)
	}
}

// extractPercent formats a ratio as percentage.
func extractPercent(data map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := data[k]; ok && v != nil {
			var n float64
			switch val := v.(type) {
			case float64:
				n = val
			case int:
				n = float64(val)
			case string:
				fmt.Sscanf(val, "%f", &n)
			}
			if n == 0 {
				continue
			}
			// If value looks like decimal (e.g. 0.35), convert to percent
			if n > -1 && n < 1 {
				n *= 100
			}
			return fmt.Sprintf("%.1f%%", n)
		}
	}
	return "-"
}

// FormatCompsTable formats CompanyMetrics into a Markdown table string.
func FormatCompsTable(metrics []CompanyMetrics) string {
	if len(metrics) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("| 公司 | 代码 | 市值 | P/E | P/S | P/B | EV/EBITDA | 营收 | 毛利率 | 营收增速 |\n")
	sb.WriteString("|------|------|------|-----|-----|-----|-----------|------|--------|----------|\n")

	for _, m := range metrics {
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			m.Name, m.Symbol, m.MarketCap, m.PE, m.PS, m.PB, m.EVEBITDA,
			m.Revenue, m.GrossMargin, m.RevenueGrowth))
	}

	return sb.String()
}

// -- HTTP helper --

func (c *Client) doRequest(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("qveris: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qveris: request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("qveris: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qveris: API returned %d: %s", resp.StatusCode, truncate(string(data), 300))
	}

	return data, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
