package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client is a Feishu Open API client.
type Client struct {
	AppID     string
	AppSecret string
	BaseURL   string // https://open.feishu.cn/open-apis or https://open.larksuite.com/open-apis
	HTTP      *http.Client

	mu          sync.RWMutex
	accessToken string
	tokenExpiry time.Time
}

// NewClient creates a new Feishu API client.
func NewClient(appID, appSecret, baseURL string) *Client {
	if baseURL == "" {
		baseURL = "https://open.feishu.cn/open-apis"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	return &Client{
		AppID:     appID,
		AppSecret: appSecret,
		BaseURL:   baseURL,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
	}
}

// IsConfigured returns true if Feishu credentials are set.
func (c *Client) IsConfigured() bool {
	return c.AppID != "" && c.AppSecret != ""
}

// -- Auth: tenant_access_token --

type tokenResponse struct {
	Code              int    `json:"code"`
	Msg               string `json:"msg"`
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int    `json:"expire"`
}

// GetAccessToken returns a valid tenant access token, refreshing if needed.
func (c *Client) GetAccessToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		token := c.accessToken
		c.mu.RUnlock()
		return token, nil
	}
	c.mu.RUnlock()

	return c.refreshToken(ctx)
}

func (c *Client) refreshToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after lock
	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.accessToken, nil
	}

	body, _ := json.Marshal(map[string]string{
		"app_id":     c.AppID,
		"app_secret": c.AppSecret,
	})

	req, err := http.NewRequestWithContext(ctx, "POST",
		c.BaseURL+"/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("feishu: create auth request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("feishu: auth request failed: %w", err)
	}
	defer resp.Body.Close()

	var result tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("feishu: parse auth response failed: %w", err)
	}

	if result.Code != 0 {
		return "", fmt.Errorf("feishu: auth failed (code=%d): %s", result.Code, result.Msg)
	}

	c.accessToken = result.TenantAccessToken
	// Refresh 5 minutes before expiry
	c.tokenExpiry = time.Now().Add(time.Duration(result.Expire-300) * time.Second)

	log.Printf("INFO: feishu token refreshed, expires in %ds", result.Expire)
	return c.accessToken, nil
}

// doAPI makes an authenticated API request.
func (c *Client) doAPI(ctx context.Context, method, path string, reqBody any) ([]byte, error) {
	token, err := c.GetAccessToken(ctx)
	if err != nil {
		return nil, err
	}

	var bodyReader io.Reader
	if reqBody != nil {
		b, _ := json.Marshal(reqBody)
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("feishu: create request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("feishu: request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("feishu: read response failed: %w", err)
	}

	return data, nil
}

// -- Search Documents --

// SearchResult represents a search hit from Feishu.
type SearchResult struct {
	DocToken string `json:"doc_token"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Type     string `json:"type"` // "doc", "wiki", "sheet", etc.
}

type searchResponse struct {
	Code int `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		HasMore   bool   `json:"has_more"`
		PageToken string `json:"page_token"`
		Entities  []struct {
			DocID   string `json:"docs_token"`
			DocType string `json:"docs_type"`
			Title   string `json:"title"`
			URL     string `json:"url"`
		} `json:"entities"`
	} `json:"data"`
}

// SearchDocs searches for documents matching the query.
func (c *Client) SearchDocs(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}

	params := url.Values{}
	params.Set("search_key", query)
	params.Set("count", fmt.Sprintf("%d", limit))
	params.Set("docs_types", `["doc","docx","wiki"]`)

	data, err := c.doAPI(ctx, "POST", "/suite/docs-api/search/object", map[string]any{
		"search_key": query,
		"count":      limit,
		"docs_types": []string{"doc", "docx", "wiki"},
	})
	if err != nil {
		return nil, err
	}

	var resp searchResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("feishu: parse search response failed: %w", err)
	}

	if resp.Code != 0 {
		return nil, fmt.Errorf("feishu: search failed (code=%d): %s", resp.Code, resp.Msg)
	}

	results := make([]SearchResult, 0, len(resp.Data.Entities))
	for _, e := range resp.Data.Entities {
		results = append(results, SearchResult{
			DocToken: e.DocID,
			Title:    e.Title,
			URL:      e.URL,
			Type:     e.DocType,
		})
	}

	return results, nil
}

// -- Fetch Document Content --

type docContentResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Content string `json:"content"`
	} `json:"data"`
}

// GetDocContent fetches the plain text content of a document.
func (c *Client) GetDocContent(ctx context.Context, docToken, docType string) (string, error) {
	var path string

	switch docType {
	case "docx":
		// Use docx API to get blocks, then extract text
		return c.getDocxContent(ctx, docToken)
	default:
		// For wiki/doc, use the raw content API
		path = fmt.Sprintf("/doc/v2/%s/raw_content", docToken)
	}

	data, err := c.doAPI(ctx, "GET", path, nil)
	if err != nil {
		return "", err
	}

	var resp docContentResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("feishu: parse doc content failed: %w", err)
	}

	if resp.Code != 0 {
		return "", fmt.Errorf("feishu: get doc content failed (code=%d): %s", resp.Code, resp.Msg)
	}

	return resp.Data.Content, nil
}

// getDocxContent fetches content from a docx document using the blocks API.
func (c *Client) getDocxContent(ctx context.Context, docToken string) (string, error) {
	path := fmt.Sprintf("/docx/v1/documents/%s/raw_content", docToken)

	data, err := c.doAPI(ctx, "GET", path, nil)
	if err != nil {
		return "", err
	}

	var resp docContentResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("feishu: parse docx content failed: %w", err)
	}

	if resp.Code != 0 {
		return "", fmt.Errorf("feishu: get docx content failed (code=%d): %s", resp.Code, resp.Msg)
	}

	return resp.Data.Content, nil
}

// -- Wiki Space --

type wikiNodeResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		HasMore   bool   `json:"has_more"`
		PageToken string `json:"page_token"`
		Items     []struct {
			SpaceID  string `json:"space_id"`
			NodeToken string `json:"node_token"`
			ObjToken string `json:"obj_token"`
			ObjType  string `json:"obj_type"`
			Title    string `json:"title"`
			HasChild bool   `json:"has_child"`
		} `json:"items"`
	} `json:"data"`
}

// WikiNode represents a node in a Feishu wiki space.
type WikiNode struct {
	NodeToken string `json:"node_token"`
	ObjToken  string `json:"obj_token"`
	ObjType   string `json:"obj_type"`
	Title     string `json:"title"`
}

// ListWikiNodes lists child nodes in a wiki space.
func (c *Client) ListWikiNodes(ctx context.Context, spaceID, parentToken string) ([]WikiNode, error) {
	path := fmt.Sprintf("/wiki/v2/spaces/%s/nodes?page_size=50", spaceID)
	if parentToken != "" {
		path += "&parent_node_token=" + parentToken
	}

	data, err := c.doAPI(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var resp wikiNodeResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("feishu: parse wiki nodes failed: %w", err)
	}

	if resp.Code != 0 {
		return nil, fmt.Errorf("feishu: list wiki nodes failed (code=%d): %s", resp.Code, resp.Msg)
	}

	nodes := make([]WikiNode, 0, len(resp.Data.Items))
	for _, item := range resp.Data.Items {
		nodes = append(nodes, WikiNode{
			NodeToken: item.NodeToken,
			ObjToken:  item.ObjToken,
			ObjType:   item.ObjType,
			Title:     item.Title,
		})
	}

	return nodes, nil
}

// GetWikiNodeContent fetches content of a wiki node.
func (c *Client) GetWikiNodeContent(ctx context.Context, nodeToken string) (string, string, error) {
	path := fmt.Sprintf("/wiki/v2/spaces/get_node?token=%s", nodeToken)

	data, err := c.doAPI(ctx, "GET", path, nil)
	if err != nil {
		return "", "", err
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Node struct {
				ObjToken string `json:"obj_token"`
				ObjType  string `json:"obj_type"`
				Title    string `json:"title"`
			} `json:"node"`
		} `json:"data"`
	}

	if err := json.Unmarshal(data, &resp); err != nil {
		return "", "", err
	}

	if resp.Code != 0 {
		return "", "", fmt.Errorf("feishu: get wiki node failed (code=%d)", resp.Code)
	}

	content, err := c.GetDocContent(ctx, resp.Data.Node.ObjToken, resp.Data.Node.ObjType)
	return resp.Data.Node.Title, content, err
}

// -- High-level: Search and Gather Reference Materials --

// ReferenceMaterial represents a piece of reference content from Feishu.
type ReferenceMaterial struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"` // truncated text
}

// GatherReferences searches Feishu for materials related to the topic and fetches their content.
func (c *Client) GatherReferences(ctx context.Context, topic string, maxDocs int) ([]ReferenceMaterial, error) {
	if !c.IsConfigured() {
		return nil, fmt.Errorf("feishu: not configured")
	}

	if maxDocs <= 0 {
		maxDocs = 5
	}

	log.Printf("INFO: feishu: searching for '%s' (max %d docs)", topic, maxDocs)

	results, err := c.SearchDocs(ctx, topic, maxDocs)
	if err != nil {
		return nil, fmt.Errorf("feishu: search failed: %w", err)
	}

	log.Printf("INFO: feishu: found %d documents", len(results))

	var materials []ReferenceMaterial
	for _, r := range results {
		content, err := c.GetDocContent(ctx, r.DocToken, r.Type)
		if err != nil {
			log.Printf("WARNING: feishu: failed to fetch doc %s (%s): %v", r.Title, r.DocToken, err)
			continue
		}

		// Truncate to ~3000 chars per document to fit in AI context
		content = truncateText(content, 3000)

		materials = append(materials, ReferenceMaterial{
			Title:   r.Title,
			URL:     r.URL,
			Content: content,
		})
	}

	log.Printf("INFO: feishu: gathered %d reference materials", len(materials))
	return materials, nil
}

func truncateText(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
