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

	toolCache map[string]*cachedTools
}

type cachedTools struct {
	tools     []searchTool
	searchID  string
	preferred int // index of known-working tool, -1 = none verified
	expires   time.Time
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
		toolCache: make(map[string]*cachedTools),
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
	ToolID      string      `json:"tool_id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Params      []toolParam `json:"params"`
	SuccessRate float64     `json:"success_rate"`
	CreditCost  int         `json:"credit_cost"`
}

// toolParam describes one parameter of a tool.
type toolParam struct {
	Name        string `json:"name"`
	Type        string `json:"type"` // string, integer, number, boolean, array, object
	Required    bool   `json:"required"`
	Description any    `json:"description"`
	Enum        []any  `json:"enum,omitempty"`
	Default     any    `json:"default,omitempty"`
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

// GetToolsByIDs fetches full schemas for specific tool IDs. The /search endpoint
// returns partial/truncated param definitions; /tools/by-ids returns the
// complete schema including nested object properties.
func (c *Client) GetToolsByIDs(ctx context.Context, toolIDs []string, searchID string) ([]searchTool, error) {
	if len(toolIDs) == 0 {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{
		"tool_ids":  toolIDs,
		"search_id": searchID,
	})

	data, err := c.doRequest(ctx, "POST", "/tools/by-ids", body)
	if err != nil {
		return nil, err
	}

	log.Printf("DEBUG: qveris tools/by-ids response: %s", truncate(string(data), 2000))

	var resp searchResponse // same shape as /search
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("qveris: parse by-ids response: %w", err)
	}
	return resp.getTools(), nil
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
		"max_response_size": 262144,
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

// getFinancialTools searches for candidate financial data tools, caches them.
func (c *Client) getFinancialTools(ctx context.Context, market string) (*cachedTools, error) {
	cacheKey := "financial-" + market
	if cached, ok := c.toolCache[cacheKey]; ok && time.Now().Before(cached.expires) {
		return cached, nil
	}

	queries := []string{
		"US stock financial data P/E ratio market cap",
		"stock fundamentals valuation metrics",
		"stock quote fundamentals financial ratios",
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

		for i, t := range tools {
			log.Printf("INFO: qveris search result %d/%d: %s (%s), %d params",
				i+1, len(tools), t.Name, t.ToolID, len(t.Params))
		}

		// Fetch FULL schemas for the top candidates. /search returns truncated
		// param lists (Hang Seng tools declare 0 params but actually require
		// stockObject); /tools/by-ids gives the complete definitions.
		topN := len(tools)
		if topN > 3 {
			topN = 3
		}
		topIDs := make([]string, 0, topN)
		for i := 0; i < topN; i++ {
			topIDs = append(topIDs, tools[i].ToolID)
		}
		if fullTools, ferr := c.GetToolsByIDs(ctx, topIDs, searchResp.SearchID); ferr == nil {
			byID := make(map[string]searchTool)
			for _, ft := range fullTools {
				byID[ft.ToolID] = ft
			}
			for i, t := range tools {
				if full, ok := byID[t.ToolID]; ok {
					tools[i] = full
					log.Printf("INFO: qveris: enriched %s with full schema (%d params, was %d)",
						full.Name, len(full.Params), len(t.Params))
					for _, p := range full.Params {
						descStr := ""
						if p.Description != nil {
							descBytes, _ := json.Marshal(p.Description)
							descStr = truncate(string(descBytes), 200)
						}
						log.Printf("INFO:   param %s (type=%s, required=%v): %s",
							p.Name, p.Type, p.Required, descStr)
					}
				}
			}
		} else {
			log.Printf("WARNING: qveris /tools/by-ids failed: %v (falling back to partial schemas)", ferr)
		}

		cached := &cachedTools{
			tools:     tools,
			searchID:  searchResp.SearchID,
			preferred: -1,
			expires:   time.Now().Add(10 * time.Minute),
		}
		c.toolCache[cacheKey] = cached
		return cached, nil
	}

	return nil, fmt.Errorf("qveris: no financial tools found for market %s", market)
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

	// A股 / 港股 use THS iFinD tools directly (hardcoded IDs) per the official
	// Qveris stock-copilot-pro skill. The /search-based routing found the
	// broken Hang Seng tools, so we bypass discovery for these markets.
	if market == "A股" || market == "港股" {
		return c.fetchThsCompsData(ctx, symbols, market)
	}

	cache, err := c.getFinancialTools(ctx, market)
	if err != nil {
		return nil, err
	}

	var results []CompanyMetrics
	for _, symbol := range symbols {
		symbol = strings.TrimSpace(symbol)
		if symbol == "" {
			continue
		}

		metrics, err := c.fetchSingleCompany(ctx, cache, symbol)
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

// parseStockCode splits "600519.SH" into ("600519", "SH") or "9988.HK" into ("09988", "HK").
// HK codes are zero-padded to 5 digits per Hang Seng convention.
func parseStockCode(symbol string) (code, market string) {
	if i := strings.Index(symbol, "."); i > 0 {
		code, market = symbol[:i], symbol[i+1:]
		if market == "HK" && len(code) < 5 {
			code = strings.Repeat("0", 5-len(code)) + code
		}
		return code, market
	}
	return symbol, ""
}

// toYahooSymbol converts our market suffix to Yahoo Finance's expected form.
// Shanghai A-shares use .SS on Yahoo (we accept .SH as input); HK tickers
// need 4-digit zero-padding ("0700.HK"); Shenzhen .SZ and US symbols are
// unchanged. Finnhub accepts the same .SS/.SZ form as Yahoo.
func toYahooSymbol(symbol string) string {
	i := strings.Index(symbol, ".")
	if i <= 0 {
		return symbol
	}
	code, market := symbol[:i], strings.ToUpper(symbol[i+1:])
	switch market {
	case "SH":
		return code + ".SS"
	case "HK":
		if len(code) < 4 {
			code = strings.Repeat("0", 4-len(code)) + code
		}
		return code + ".HK"
	default:
		return symbol
	}
}

// toThsCode formats a symbol for THS iFinD (同花顺) — the provider Qveris uses
// for A-share and Hong Kong data. Logic mirrors the official
// stock-copilot-pro/toThsCode: if the symbol already has an exchange suffix,
// uppercase it and (for HK) zero-pad the numeric part to 4 digits; otherwise
// infer by prefix (6* -> .SH, else .SZ for CN).
func toThsCode(symbol, market string) string {
	upper := strings.ToUpper(strings.TrimSpace(symbol))
	if upper == "" {
		return upper
	}
	if i := strings.Index(upper, "."); i > 0 {
		code, suffix := upper[:i], upper[i+1:]
		if suffix == "HK" {
			stripped := strings.TrimLeft(code, "0")
			if stripped == "" {
				stripped = "0"
			}
			if len(stripped) < 4 {
				stripped = strings.Repeat("0", 4-len(stripped)) + stripped
			}
			return stripped + ".HK"
		}
		return upper
	}
	switch market {
	case "A股", "CN":
		if strings.HasPrefix(upper, "6") {
			return upper + ".SH"
		}
		return upper + ".SZ"
	case "港股", "HK":
		stripped := strings.TrimLeft(upper, "0")
		if stripped == "" {
			stripped = "0"
		}
		if len(stripped) < 4 {
			stripped = strings.Repeat("0", 4-len(stripped)) + stripped
		}
		return stripped + ".HK"
	}
	return upper
}

// THS iFinD tool IDs (confirmed via Qveris open-qveris-skills/stock-copilot-pro).
const (
	thsQuoteToolID  = "ths_ifind.real_time_quotation.v1"
	thsBasicsToolID = "ths_ifind.company_basics.v1"
)

// fetchThsCompsData fetches A-share / HK comps data using THS iFinD tools
// directly. It calls the real-time quotation for price/PE/PB/marketCap, then
// company_basics for the Chinese name, industry, and revenue/net income.
func (c *Client) fetchThsCompsData(ctx context.Context, symbols []string, market string) ([]CompanyMetrics, error) {
	var results []CompanyMetrics
	for _, raw := range symbols {
		sym := strings.TrimSpace(raw)
		if sym == "" {
			continue
		}
		code := toThsCode(sym, market)
		log.Printf("INFO: ths_ifind: fetching %s (code=%s)", sym, code)

		m := CompanyMetrics{
			Symbol: sym, Name: sym, Source: "THS iFinD",
			MarketCap: "-", Price: "-", PE: "-", PS: "-", PB: "-",
			EVEBITDA: "-", Revenue: "-", NetIncome: "-",
			GrossMargin: "-", RevenueGrowth: "-",
		}

		params := map[string]any{"codes": code}
		if resp, err := c.ExecuteTool(ctx, thsQuoteToolID, "", params); err == nil {
			if row := pickThsRow(c.resolveResult(ctx, resp.Result)); row != nil {
				m.Price = extractString(row, "latest", "close", "price")
				m.MarketCap = extractMoney(row, "mv", "totalCapital", "marketCap")
				m.PE = extractNumber(row, "pe_ttm", "pe")
				m.PB = extractNumber(row, "pb", "pbr_lf")
				if n := extractString(row, "ths_corp_cn_name_stock", "ths_short_name_stock", "short_name"); n != "-" {
					m.Name = n
				}
			}
		} else {
			log.Printf("WARNING: ths_ifind quotation failed for %s: %v", code, err)
		}

		if resp, err := c.ExecuteTool(ctx, thsBasicsToolID, "", params); err == nil {
			if row := pickThsRow(c.resolveResult(ctx, resp.Result)); row != nil {
				if m.Name == "-" || m.Name == sym {
					if n := extractString(row, "ths_corp_cn_name_stock", "ths_short_name_stock", "short_name"); n != "-" {
						m.Name = n
					}
				}
				if m.Revenue == "-" {
					m.Revenue = extractMoney(row, "ths_revenue_stock", "ths_operating_total_revenue_stock")
				}
				if m.NetIncome == "-" {
					m.NetIncome = extractMoney(row, "ths_np_atoopc_stock", "ths_np_stock")
				}
			}
		} else {
			log.Printf("WARNING: ths_ifind basics failed for %s: %v", code, err)
		}

		if m.MarketCap == "-" && m.PE == "-" && m.Price == "-" && m.Revenue == "-" {
			m.Source = "数据不可用"
		}
		results = append(results, m)
		time.Sleep(200 * time.Millisecond)
	}
	return results, nil
}

// pickThsRow extracts the first row from a THS-shaped response:
// { result: { data: [[{row}]] } } or { data: [[{row}]] } or { data: [{row}] }.
func pickThsRow(raw json.RawMessage) map[string]any {
	var wrapper map[string]any
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil
	}
	containers := []map[string]any{wrapper}
	if inner, ok := wrapper["result"].(map[string]any); ok {
		containers = append(containers, inner)
	}
	for _, container := range containers {
		d, ok := container["data"]
		if !ok {
			continue
		}
		if row := firstThsRow(d); row != nil {
			return row
		}
	}
	return nil
}

func firstThsRow(v any) map[string]any {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	if inner, ok := arr[0].([]any); ok {
		if len(inner) == 0 {
			return nil
		}
		if row, ok := inner[0].(map[string]any); ok {
			return row
		}
		return nil
	}
	if row, ok := arr[0].(map[string]any); ok {
		return row
	}
	return nil
}

// buildParamsFromSchema builds parameters using the tool's declared schema.
// Only includes params that the tool actually declares, with correct types.
func buildParamsFromSchema(tool *searchTool, symbol string) map[string]any {
	params := make(map[string]any)
	code, market := parseStockCode(symbol)
	// For Finnhub/Yahoo tools (the path we take for all markets now), A股/港股
	// tickers must be translated to Yahoo's convention (.SS / padded .HK).
	tickerSymbol := toYahooSymbol(symbol)

	for _, p := range tool.Params {
		nameL := strings.ToLower(p.Name)

		// Compound stock object params (Hang Seng pattern)
		if nameL == "stockobject" {
			if market != "" {
				params[p.Name] = map[string]any{"code": code, "market": market}
			} else {
				params[p.Name] = map[string]any{"code": code}
			}
			continue
		}

		// Symbol-like params
		if contains(nameL, []string{"symbol", "ticker", "code", "stock", "secucode", "instrument"}) {
			params[p.Name] = tickerSymbol
			continue
		}

		// Market/exchange params
		if contains(nameL, []string{"market", "exchange"}) {
			params[p.Name] = market
			continue
		}

		// Known required params with common defaults
		if p.Required {
			switch nameL {
			case "metric":
				params[p.Name] = "all"
			case "function":
				params[p.Name] = "OVERVIEW"
			case "query":
				params[p.Name] = tickerSymbol
			case "pageno":
				params[p.Name] = 1
			case "pagesize":
				params[p.Name] = 10
			case "period":
				params[p.Name] = "annual"
			default:
				// If it has an enum, pick the first option
				if len(p.Enum) > 0 {
					params[p.Name] = p.Enum[0]
				} else if p.Default != nil {
					params[p.Name] = p.Default
				}
			}
		}
	}

	// Hang Seng tools require stockObject/stockobject even when the returned
	// schema doesn't declare it. Different tools use different casings
	// (A-share uses "stockobject", HK uses "stockObject") — send both so the
	// server accepts either without a second roundtrip.
	if strings.HasPrefix(tool.ToolID, "hangseng_") {
		stockObj := map[string]any{"code": code}
		if market != "" {
			stockObj["market"] = market
		}
		if _, has := params["stockObject"]; !has {
			params["stockObject"] = stockObj
		}
		if _, has := params["stockobject"]; !has {
			params["stockobject"] = stockObj
		}
	}

	return params
}

func contains(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func (c *Client) fetchSingleCompany(ctx context.Context, cache *cachedTools, symbol string) (*CompanyMetrics, error) {
	cleanSymbol := strings.TrimSpace(symbol)

	// Build tool order: preferred first, then others (max 3 attempts).
	order := make([]int, 0, len(cache.tools))
	if cache.preferred >= 0 {
		order = append(order, cache.preferred)
	}
	for i := range cache.tools {
		if i != cache.preferred {
			order = append(order, i)
		}
	}
	if len(order) > 3 {
		order = order[:3]
	}

	var lastErr error
	for _, idx := range order {
		tool := &cache.tools[idx]
		params := buildParamsFromSchema(tool, cleanSymbol)

		log.Printf("INFO: qveris trying tool %d/%d %s (%s) for '%s' with params: %v",
			idx+1, len(cache.tools), tool.Name, tool.ToolID, cleanSymbol, params)

		resp, err := c.ExecuteTool(ctx, tool.ToolID, cache.searchID, params)
		if err != nil {
			log.Printf("WARNING: qveris tool %s failed for %s: %v, trying next", tool.Name, cleanSymbol, err)
			lastErr = err
			continue
		}

		cache.preferred = idx

		resultData := c.resolveResult(ctx, resp.Result)
		rawData := flattenResult(resultData)

		metrics := &CompanyMetrics{
			Symbol:        symbol,
			Name:          extractString(rawData, "name", "company_name", "shortName", "longName", "companyName"),
			MarketCap:     extractMoney(rawData, "market_cap", "marketCap", "mktCap", "market_capitalization", "marketCapitalization"),
			Price:         extractString(rawData, "price", "current_price", "currentPrice", "regularMarketPrice", "last_price", "latestPrice"),
			PE:            extractNumber(rawData, "pe_ratio", "pe", "trailingPE", "peRatio", "price_earnings_ratio", "peTTM", "peBasicExclExtraTTM", "peNormalizedAnnual", "peAnnual"),
			PS:            extractNumber(rawData, "ps_ratio", "ps", "priceToSales", "psRatio", "price_to_sales", "psTTM", "psAnnual"),
			PB:            extractNumber(rawData, "pb_ratio", "pb", "priceToBook", "pbRatio", "price_to_book", "pbQuarterly", "pbAnnual"),
			EVEBITDA:      extractNumber(rawData, "ev_ebitda", "evEbitda", "enterpriseToEbitda", "ev_to_ebitda"),
			Revenue:       extractMoney(rawData, "revenue", "totalRevenue", "total_revenue", "revenueTtm", "revenueTTM"),
			NetIncome:     extractMoney(rawData, "net_income", "netIncome", "net_profit", "netIncomeTtm"),
			GrossMargin:   extractPercent(rawData, "gross_margin", "grossMargin", "grossMargins", "gross_profit_margin", "grossMarginTTM", "grossMarginAnnual"),
			RevenueGrowth: extractPercent(rawData, "revenue_growth", "revenueGrowth", "revenue_growth_yoy", "revenueGrowthTTMYoy", "revenueGrowth5Y", "revenueGrowthQuarterlyYoy"),
			Source:        "Qveris.ai",
		}

		if metrics.Name == "-" || metrics.Name == "" {
			metrics.Name = symbol
		}

		if metrics.MarketCap == "-" && metrics.PE == "-" && metrics.Price == "-" && metrics.Revenue == "-" {
			log.Printf("WARNING: qveris returned no usable data for %s, raw: %s", symbol, truncate(string(resp.Result), 300))
			metrics.Source = "数据不可用"
		}

		return metrics, nil
	}

	return nil, lastErr
}

// resolveResult downloads full content if the Qveris response was truncated.
func (c *Client) resolveResult(ctx context.Context, result json.RawMessage) json.RawMessage {
	var wrapper map[string]any
	if err := json.Unmarshal(result, &wrapper); err != nil {
		return result
	}
	url, _ := wrapper["full_content_file_url"].(string)
	if url == "" {
		return result
	}

	log.Printf("INFO: qveris: response truncated, downloading full content")
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return result
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		log.Printf("WARNING: qveris: failed to download full content: %v", err)
		return result
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return result
	}
	log.Printf("INFO: qveris: downloaded full content (%d bytes)", len(data))
	return json.RawMessage(data)
}

// flattenResult tries to unwrap common nested response shapes.
// Runs two passes so deeply nested shapes like {"data":{"metric":{...}}} are
// fully surfaced to the top level.
func flattenResult(data json.RawMessage) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]any{}
	}
	keys := []string{"data", "result", "quote", "summary", "metric", "metrics", "defaultKeyStatistics", "financialData"}
	for pass := 0; pass < 2; pass++ {
		for _, key := range keys {
			if nested, ok := m[key]; ok {
				if nestedMap, ok := nested.(map[string]any); ok && len(nestedMap) > 0 {
					for k, v := range nestedMap {
						m[k] = v
					}
				}
			}
		}
	}
	return m
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
