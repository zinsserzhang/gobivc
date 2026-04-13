package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// Client wraps the @larksuite/cli (lark-cli) for Feishu/Lark operations.
type Client struct {
	CLIPath string // path to lark-cli binary, defaults to "lark-cli"
	enabled bool
}

// NewClient creates a new Feishu CLI client.
func NewClient() *Client {
	cliPath := "lark-cli"
	// Check if lark-cli is available
	_, err := exec.LookPath(cliPath)
	if err != nil {
		// Try npx fallback
		_, err2 := exec.LookPath("npx")
		if err2 == nil {
			cliPath = "npx"
		}
	}
	return &Client{CLIPath: cliPath}
}

// CheckAvailable verifies lark-cli is installed and authenticated.
func (c *Client) CheckAvailable(ctx context.Context) bool {
	var out []byte
	var err error

	if c.CLIPath == "npx" {
		out, err = exec.CommandContext(ctx, "npx", "@larksuite/cli", "auth", "status", "--output", "json").CombinedOutput()
	} else {
		out, err = exec.CommandContext(ctx, c.CLIPath, "auth", "status", "--output", "json").CombinedOutput()
	}

	if err != nil {
		log.Printf("INFO: feishu: lark-cli not available: %v", err)
		c.enabled = false
		return false
	}

	outStr := string(out)
	// lark-cli is available if we can see any identity (user or bot)
	if strings.Contains(outStr, "appId") || strings.Contains(outStr, "open_id") || strings.Contains(outStr, "identity") {
		c.enabled = true
		log.Printf("INFO: feishu: lark-cli available, auth output: %s", strings.TrimSpace(outStr))
		return true
	}

	log.Printf("INFO: feishu: lark-cli auth status: %s", outStr)
	c.enabled = false
	return false
}

// IsConfigured returns true if lark-cli is available and authenticated.
func (c *Client) IsConfigured() bool {
	return c.enabled
}

// run executes a lark-cli command and returns stdout.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	var cmd *exec.Cmd
	if c.CLIPath == "npx" {
		fullArgs := append([]string{"@larksuite/cli"}, args...)
		cmd = exec.CommandContext(ctx, "npx", fullArgs...)
	} else {
		cmd = exec.CommandContext(ctx, c.CLIPath, args...)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("lark-cli %s failed: %w\noutput: %s", strings.Join(args, " "), err, string(out))
	}
	return out, nil
}

// -- Search Documents --

// SearchResult represents a search hit.
type SearchResult struct {
	Token string `json:"docs_token"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Type  string `json:"docs_type"`
}

// SearchDocs searches documents and wiki using lark-cli docs +search.
func (c *Client) SearchDocs(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}

	// Use the shortcut command for docs search
	out, err := c.run(ctx, "docs", "+search",
		"--query", query,
		"--count", fmt.Sprintf("%d", limit),
		"--output", "json",
	)
	if err != nil {
		// Fallback: try the API layer
		return c.searchDocsAPI(ctx, query, limit)
	}

	return parseSearchResults(out)
}

// searchDocsAPI uses the raw API layer as fallback.
func (c *Client) searchDocsAPI(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	data := fmt.Sprintf(`{"search_key":"%s","count":%d,"docs_types":["doc","docx","wiki"]}`,
		escapeJSON(query), limit)

	out, err := c.run(ctx, "api", "POST",
		"/open-apis/suite/docs-api/search/object",
		"--data", data,
		"--output", "json",
	)
	if err != nil {
		return nil, err
	}

	return parseSearchResults(out)
}

func parseSearchResults(data []byte) ([]SearchResult, error) {
	// Try to parse as a top-level response with data.entities
	var resp struct {
		Data struct {
			Entities []SearchResult `json:"entities"`
		} `json:"data"`
		// Also handle flat array output from shortcuts
	}

	if err := json.Unmarshal(data, &resp); err == nil && len(resp.Data.Entities) > 0 {
		return resp.Data.Entities, nil
	}

	// Try flat array
	var results []SearchResult
	if err := json.Unmarshal(data, &results); err == nil && len(results) > 0 {
		return results, nil
	}

	// Try to find JSON in the output (CLI may have extra text)
	jsonStart := strings.Index(string(data), "{")
	jsonArrayStart := strings.Index(string(data), "[")

	start := -1
	if jsonStart >= 0 && (jsonArrayStart < 0 || jsonStart < jsonArrayStart) {
		start = jsonStart
	} else if jsonArrayStart >= 0 {
		start = jsonArrayStart
	}

	if start >= 0 {
		trimmed := data[start:]
		if err := json.Unmarshal(trimmed, &resp); err == nil && len(resp.Data.Entities) > 0 {
			return resp.Data.Entities, nil
		}
		if err := json.Unmarshal(trimmed, &results); err == nil {
			return results, nil
		}
	}

	return nil, fmt.Errorf("feishu: could not parse search results: %s", truncateStr(string(data), 200))
}

// -- Fetch Document Content --

// GetDocContent fetches the content of a document by token and type.
func (c *Client) GetDocContent(ctx context.Context, docToken, docType string) (string, error) {
	// Try shortcut command first
	out, err := c.run(ctx, "docs", "+read",
		"--document-id", docToken,
		"--output", "json",
	)
	if err == nil {
		content := extractContent(out)
		if content != "" {
			return content, nil
		}
	}

	// Fallback: use raw API based on doc type
	var path string
	switch docType {
	case "docx":
		path = fmt.Sprintf("/open-apis/docx/v1/documents/%s/raw_content", docToken)
	default:
		path = fmt.Sprintf("/open-apis/doc/v2/%s/raw_content", docToken)
	}

	out, err = c.run(ctx, "api", "GET", path, "--output", "json")
	if err != nil {
		return "", err
	}

	return extractContent(out), nil
}

func extractContent(data []byte) string {
	// Try {"data":{"content":"..."}} format
	var resp struct {
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err == nil && resp.Data.Content != "" {
		return resp.Data.Content
	}

	// Try {"content":"..."} format
	var simple struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(data, &simple); err == nil && simple.Content != "" {
		return simple.Content
	}

	// Return raw string if it looks like text content
	s := strings.TrimSpace(string(data))
	if len(s) > 0 && s[0] != '{' && s[0] != '<' {
		return s
	}

	return ""
}

// -- Wiki Operations --

// ListWikiNodes lists nodes in a wiki space using lark-cli.
func (c *Client) ListWikiNodes(ctx context.Context, spaceID string) ([]WikiNode, error) {
	out, err := c.run(ctx, "api", "GET",
		fmt.Sprintf("/open-apis/wiki/v2/spaces/%s/nodes?page_size=50", spaceID),
		"--output", "json",
	)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			Items []WikiNode `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("feishu: parse wiki nodes failed: %w", err)
	}

	return resp.Data.Items, nil
}

// WikiNode represents a wiki knowledge base node.
type WikiNode struct {
	NodeToken string `json:"node_token"`
	ObjToken  string `json:"obj_token"`
	ObjType   string `json:"obj_type"`
	Title     string `json:"title"`
}

// -- High-level: Gather Reference Materials --

// ReferenceMaterial represents a piece of reference content from Feishu.
type ReferenceMaterial struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

// GatherReferences searches Feishu for materials related to the topic.
func (c *Client) GatherReferences(ctx context.Context, topic string, maxDocs int) ([]ReferenceMaterial, error) {
	if !c.enabled {
		return nil, fmt.Errorf("feishu: lark-cli not available")
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
		content, err := c.GetDocContent(ctx, r.Token, r.Type)
		if err != nil {
			log.Printf("WARNING: feishu: failed to fetch doc '%s' (%s): %v", r.Title, r.Token, err)
			continue
		}

		// Truncate to ~3000 chars per document
		content = truncateText(content, 3000)
		if content == "" {
			continue
		}

		materials = append(materials, ReferenceMaterial{
			Title:   r.Title,
			URL:     r.URL,
			Content: content,
		})
	}

	log.Printf("INFO: feishu: gathered %d reference materials", len(materials))
	return materials, nil
}

// -- Utilities --

func truncateText(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	// Remove surrounding quotes
	return string(b[1 : len(b)-1])
}
