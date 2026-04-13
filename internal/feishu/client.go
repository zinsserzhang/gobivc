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
	CLIPath     string // path to lark-cli binary, defaults to "lark-cli"
	FolderToken string // specific folder to search within (empty = global search)
	enabled     bool
}

// NewClient creates a new Feishu CLI client.
// folderToken limits searches to a specific Drive folder (empty = global).
func NewClient(folderToken string) *Client {
	cliPath := "lark-cli"
	_, err := exec.LookPath(cliPath)
	if err != nil {
		_, err2 := exec.LookPath("npx")
		if err2 == nil {
			cliPath = "npx"
		}
	}
	return &Client{CLIPath: cliPath, FolderToken: folderToken}
}

// CheckAvailable verifies lark-cli is installed and authenticated.
func (c *Client) CheckAvailable(ctx context.Context) bool {
	var cmd *exec.Cmd
	if c.CLIPath == "npx" {
		cmd = exec.CommandContext(ctx, "npx", "@larksuite/cli", "auth", "status")
	} else {
		cmd = exec.CommandContext(ctx, c.CLIPath, "auth", "status")
	}

	// lark-cli may exit with non-zero even when it outputs valid JSON,
	// so ignore the error and just check the combined output.
	out, _ := cmd.CombinedOutput()
	outStr := string(out)

	if strings.Contains(outStr, "appId") || strings.Contains(outStr, "identity") {
		c.enabled = true
		log.Printf("INFO: feishu: lark-cli authenticated")
		return true
	}

	// Check if lark-cli binary exists at all
	if _, lookErr := exec.LookPath(c.CLIPath); lookErr != nil {
		log.Printf("INFO: feishu: lark-cli binary not found")
	} else {
		log.Printf("INFO: feishu: lark-cli auth check output: %s", strings.TrimSpace(outStr))
	}

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

	// Use Output() to only capture stdout, ignoring stderr warnings
	out, err := cmd.Output()
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

// -- Contacts / Org Search --

// Contact represents a person in the Feishu organization.
type Contact struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
}

// SearchContacts searches for users in the Feishu organization.
func (c *Client) SearchContacts(ctx context.Context, query string) ([]Contact, error) {
	if !c.enabled {
		return nil, fmt.Errorf("feishu: lark-cli not available")
	}

	// Use lark-cli contact search
	out, err := c.run(ctx, "api", "POST",
		"/open-apis/search/v1/user",
		"--data", fmt.Sprintf(`{"query":"%s","page_size":10}`, escapeJSON(query)),
		"--params", `{"user_id_type":"open_id"}`,
	)
	if err != nil {
		// Fallback: try the contact search shortcut
		out, err = c.run(ctx, "api", "GET",
			fmt.Sprintf("/open-apis/contact/v3/users?user_id_type=open_id&page_size=10"),
		)
		if err != nil {
			return nil, fmt.Errorf("feishu: contact search failed: %w", err)
		}
	}

	return parseContacts(out)
}

func parseContacts(data []byte) ([]Contact, error) {
	cleaned := extractJSON(data)

	// Try search API response format
	var searchResp struct {
		Data struct {
			Users []struct {
				OpenID string `json:"open_id"`
				Name   string `json:"name"`
				Avatar struct {
					URL string `json:"avatar_72"`
				} `json:"avatar"`
			} `json:"users"`
		} `json:"data"`
	}
	if err := json.Unmarshal(cleaned, &searchResp); err == nil && len(searchResp.Data.Users) > 0 {
		contacts := make([]Contact, 0, len(searchResp.Data.Users))
		for _, u := range searchResp.Data.Users {
			contacts = append(contacts, Contact{
				ID:     u.OpenID,
				Name:   u.Name,
				Avatar: u.Avatar.URL,
			})
		}
		return contacts, nil
	}

	// Try contact list response format
	var listResp struct {
		Data struct {
			Items []struct {
				OpenID string `json:"open_id"`
				Name   string `json:"name"`
				Avatar struct {
					URL string `json:"avatar_72"`
				} `json:"avatar"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(cleaned, &listResp); err == nil && len(listResp.Data.Items) > 0 {
		contacts := make([]Contact, 0, len(listResp.Data.Items))
		for _, u := range listResp.Data.Items {
			contacts = append(contacts, Contact{
				ID:     u.OpenID,
				Name:   u.Name,
				Avatar: u.Avatar.URL,
			})
		}
		return contacts, nil
	}

	return []Contact{}, nil
}

// -- High-level: Gather Reference Materials --

// ReferenceMaterial represents a piece of reference content from Feishu.
type ReferenceMaterial struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

// DriveFile represents a file in a Feishu Drive folder.
type DriveFile struct {
	Token string `json:"token"`
	Name  string `json:"name"`
	Type  string `json:"type"` // "docx", "doc", "sheet", "folder", etc.
	URL   string `json:"url"`
}

// ListFolderFiles lists all files in a Drive folder.
func (c *Client) ListFolderFiles(ctx context.Context, folderToken string) ([]DriveFile, error) {
	out, err := c.run(ctx, "api", "GET",
		fmt.Sprintf("/open-apis/drive/v1/files?folder_token=%s&page_size=50", folderToken),
		"--output", "json",
	)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			Files []DriveFile `json:"files"`
		} `json:"data"`
	}

	// Try to parse, handling possible extra text in output
	cleaned := extractJSON(out)
	if err := json.Unmarshal(cleaned, &resp); err != nil {
		return nil, fmt.Errorf("feishu: parse folder files failed: %w, output: %s", err, truncateStr(string(out), 200))
	}

	return resp.Data.Files, nil
}

// extractJSON finds the first { or [ in data and returns from there.
func extractJSON(data []byte) []byte {
	s := string(data)
	i := strings.IndexAny(s, "{[")
	if i >= 0 {
		return []byte(s[i:])
	}
	return data
}

// GatherReferences fetches materials from the configured folder or via global search.
func (c *Client) GatherReferences(ctx context.Context, topic string, maxDocs int) ([]ReferenceMaterial, error) {
	if !c.enabled {
		return nil, fmt.Errorf("feishu: lark-cli not available")
	}

	if maxDocs <= 0 {
		maxDocs = 5
	}

	log.Printf("INFO: feishu: gathering references for '%s' (max %d)", topic, maxDocs)

	// If a specific folder is configured, list its files and match by topic
	if c.FolderToken != "" {
		return c.gatherFromFolder(ctx, topic, maxDocs)
	}

	// Otherwise fall back to global search
	return c.gatherFromSearch(ctx, topic, maxDocs)
}

// gatherFromFolder lists files in the configured folder and fetches relevant ones.
func (c *Client) gatherFromFolder(ctx context.Context, topic string, maxDocs int) ([]ReferenceMaterial, error) {
	files, err := c.ListFolderFiles(ctx, c.FolderToken)
	if err != nil {
		log.Printf("WARNING: feishu: folder listing failed, falling back to search: %v", err)
		return c.gatherFromSearch(ctx, topic, maxDocs)
	}

	log.Printf("INFO: feishu: found %d files in folder", len(files))

	// Filter to document types only
	var docs []DriveFile
	for _, f := range files {
		switch f.Type {
		case "docx", "doc", "wiki", "sheet":
			docs = append(docs, f)
		}
	}

	// Score and rank by topic relevance (simple keyword matching on title)
	type scored struct {
		file  DriveFile
		score int
	}
	topicLower := strings.ToLower(topic)
	topicWords := strings.Fields(topicLower)

	var scored_files []scored
	for _, d := range docs {
		nameLower := strings.ToLower(d.Name)
		score := 0
		// Full topic match
		if strings.Contains(nameLower, topicLower) {
			score += 10
		}
		// Individual word matches
		for _, w := range topicWords {
			if len(w) >= 2 && strings.Contains(nameLower, w) {
				score += 3
			}
		}
		// Always include (score 0 = no match but still in the folder)
		scored_files = append(scored_files, scored{file: d, score: score})
	}

	// Sort by score descending
	for i := 0; i < len(scored_files); i++ {
		for j := i + 1; j < len(scored_files); j++ {
			if scored_files[j].score > scored_files[i].score {
				scored_files[i], scored_files[j] = scored_files[j], scored_files[i]
			}
		}
	}

	// Take top N
	if len(scored_files) > maxDocs {
		scored_files = scored_files[:maxDocs]
	}

	var materials []ReferenceMaterial
	for _, sf := range scored_files {
		content, err := c.GetDocContent(ctx, sf.file.Token, sf.file.Type)
		if err != nil {
			log.Printf("WARNING: feishu: failed to fetch '%s': %v", sf.file.Name, err)
			continue
		}

		content = truncateText(content, 3000)
		if content == "" {
			continue
		}

		materials = append(materials, ReferenceMaterial{
			Title:   sf.file.Name,
			URL:     sf.file.URL,
			Content: content,
		})
	}

	log.Printf("INFO: feishu: gathered %d reference materials from folder", len(materials))
	return materials, nil
}

// gatherFromSearch uses global document search.
func (c *Client) gatherFromSearch(ctx context.Context, topic string, maxDocs int) ([]ReferenceMaterial, error) {
	results, err := c.SearchDocs(ctx, topic, maxDocs)
	if err != nil {
		return nil, fmt.Errorf("feishu: search failed: %w", err)
	}

	log.Printf("INFO: feishu: search found %d documents", len(results))

	var materials []ReferenceMaterial
	for _, r := range results {
		content, err := c.GetDocContent(ctx, r.Token, r.Type)
		if err != nil {
			log.Printf("WARNING: feishu: failed to fetch '%s': %v", r.Title, err)
			continue
		}

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

	log.Printf("INFO: feishu: gathered %d reference materials from search", len(materials))
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
