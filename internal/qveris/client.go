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
}

// NewClient creates a new Qveris API client.
func NewClient(apiKey string) *Client {
	baseURL := "https://qveris.ai/api/v1"
	if strings.HasPrefix(apiKey, "sk-cn-") {
		baseURL = "https://qveris.cn/api/v1"
	}
	return &Client{
		APIKey:  apiKey,
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// IsConfigured returns true if the API key is set.
func (c *Client) IsConfigured() bool {
	return c.APIKey != ""
}

// -- Search for tools --

type searchResponse struct {
	SearchID string       `json:"search_id"`
	Tools    []searchTool `json:"tools"`
}

type searchTool struct {
	ToolID      string `json:"tool_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// SearchTools finds available tools matching a query.
func (c *Client) SearchTools(ctx context.Context, query string, limit int) (*searchResponse, error) {
	if limit <= 0 {
		limit = 5
	}
	body, _ := json.Marshal(map[string]any{
		"query": query,
		"limit": limit,
	})

	data, err := c.doRequest(ctx, "POST", "/search", body)
	if err != nil {
		return nil, err
	}

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
		"max_response_size": 20480,
	})

	data, err := c.doRequest(ctx, "POST", "/tools/execute?tool_id="+toolID, body)
	if err != nil {
		return nil, err
	}

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
	Symbol     string  `json:"symbol"`
	Name       string  `json:"name"`
	MarketCap  string  `json:"market_cap"`
	PE         string  `json:"pe_ratio"`
	PS         string  `json:"ps_ratio"`
	PB         string  `json:"pb_ratio"`
	EVEBITDA   string  `json:"ev_ebitda"`
	Revenue    string  `json:"revenue"`
	NetIncome  string  `json:"net_income"`
	GrossMargin string `json:"gross_margin"`
	RevenueGrowth string `json:"revenue_growth"`
}

// FetchCompsData fetches financial metrics for a list of company symbols.
func (c *Client) FetchCompsData(ctx context.Context, symbols []string) ([]CompanyMetrics, error) {
	if !c.IsConfigured() {
		return nil, fmt.Errorf("qveris: API key not configured")
	}

	log.Printf("INFO: qveris: fetching comps data for %d companies", len(symbols))

	// Step 1: Search for a financial metrics tool
	searchResp, err := c.SearchTools(ctx, "stock financial metrics valuation PE ratio market cap revenue", 5)
	if err != nil {
		return nil, fmt.Errorf("qveris: search for financial tools failed: %w", err)
	}

	if len(searchResp.Tools) == 0 {
		return nil, fmt.Errorf("qveris: no financial data tools found")
	}

	toolID := searchResp.Tools[0].ToolID
	searchID := searchResp.SearchID
	log.Printf("INFO: qveris: using tool '%s' (%s)", searchResp.Tools[0].Name, toolID)

	// Step 2: Execute for each symbol
	var results []CompanyMetrics
	for _, symbol := range symbols {
		symbol = strings.TrimSpace(symbol)
		if symbol == "" {
			continue
		}

		metrics, err := c.fetchSingleCompany(ctx, toolID, searchID, symbol)
		if err != nil {
			log.Printf("WARNING: qveris: failed to fetch data for %s: %v", symbol, err)
			// Add placeholder with just the symbol
			results = append(results, CompanyMetrics{Symbol: symbol, Name: symbol})
			continue
		}
		results = append(results, *metrics)
	}

	log.Printf("INFO: qveris: fetched comps data for %d/%d companies", len(results), len(symbols))
	return results, nil
}

func (c *Client) fetchSingleCompany(ctx context.Context, toolID, searchID, symbol string) (*CompanyMetrics, error) {
	resp, err := c.ExecuteTool(ctx, toolID, searchID, map[string]any{
		"symbol": symbol,
		"ticker": symbol,
	})
	if err != nil {
		return nil, err
	}

	// Parse the result - structure varies by tool, try to extract common fields
	var rawData map[string]any
	if err := json.Unmarshal(resp.Result, &rawData); err != nil {
		return nil, fmt.Errorf("parse result: %w", err)
	}

	metrics := &CompanyMetrics{
		Symbol:        symbol,
		Name:          extractString(rawData, "name", "company_name", "shortName"),
		MarketCap:     extractString(rawData, "market_cap", "marketCap", "mktCap"),
		PE:            extractString(rawData, "pe_ratio", "pe", "trailingPE", "peRatio"),
		PS:            extractString(rawData, "ps_ratio", "ps", "priceToSales", "psRatio"),
		PB:            extractString(rawData, "pb_ratio", "pb", "priceToBook", "pbRatio"),
		EVEBITDA:      extractString(rawData, "ev_ebitda", "evEbitda", "enterpriseToEbitda"),
		Revenue:       extractString(rawData, "revenue", "totalRevenue"),
		NetIncome:     extractString(rawData, "net_income", "netIncome"),
		GrossMargin:   extractString(rawData, "gross_margin", "grossMargin", "grossMargins"),
		RevenueGrowth: extractString(rawData, "revenue_growth", "revenueGrowth"),
	}

	if metrics.Name == "" {
		metrics.Name = symbol
	}

	return metrics, nil
}

// extractString tries multiple keys in a map and returns the first non-empty value as string.
func extractString(data map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := data[k]; ok && v != nil {
			return fmt.Sprintf("%v", v)
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
	sb.WriteString("\n## 二级市场 Comps 对比\n\n")
	sb.WriteString("| 公司 | 代码 | 市值 | P/E | P/S | P/B | EV/EBITDA | 营收 | 净利润 | 毛利率 | 营收增速 |\n")
	sb.WriteString("|------|------|------|-----|-----|-----|-----------|------|--------|--------|----------|\n")

	for _, m := range metrics {
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			m.Name, m.Symbol, m.MarketCap, m.PE, m.PS, m.PB, m.EVEBITDA,
			m.Revenue, m.NetIncome, m.GrossMargin, m.RevenueGrowth))
	}

	sb.WriteString("\n*数据来源: Qveris.ai*\n")
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
		return nil, fmt.Errorf("qveris: API returned %d: %s", resp.StatusCode, string(data[:min(len(data), 200)]))
	}

	return data, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
