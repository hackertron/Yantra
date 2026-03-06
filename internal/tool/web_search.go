package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

const (
	webSearchTimeout        = 30 * time.Second
	webSearchMaxBody        = 1024 * 1024 // 1 MB
	webSearchDefaultCount   = 5
	webSearchMaxCount       = 10
	webSearchMaxSnippetLen  = 2000

	duckDuckGoBaseURL = "https://api.duckduckgo.com/"
	googleBaseURL     = "https://www.googleapis.com/customsearch/v1"
)

type webSearchTool struct {
	cfg types.WebSearchConfig
}

func NewWebSearch(cfg types.WebSearchConfig) types.Tool {
	return &webSearchTool{cfg: cfg}
}

func (t *webSearchTool) Name() string                 { return "web_search" }
func (t *webSearchTool) SafetyTier() types.SafetyTier { return types.SideEffecting }
func (t *webSearchTool) Timeout() time.Duration       { return webSearchTimeout }

func (t *webSearchTool) Description() string {
	return "Search the web and return a list of results with titles, URLs, and snippets."
}

func (t *webSearchTool) Decl() types.FunctionDecl {
	return types.FunctionDecl{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: Schema(
			Prop{Name: "query", Type: TypeString, Description: "Search query", Required: true},
			Prop{Name: "count", Type: TypeInteger, Description: "Number of results to return (default 5, max 10)", Required: false},
		),
	}
}

func (t *webSearchTool) Execute(ctx context.Context, input json.RawMessage, execCtx types.ToolExecutionContext) (string, error) {
	var args struct {
		Query string  `json:"query"`
		Count *int    `json:"count"` // pointer to distinguish absent/null from 0
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if strings.TrimSpace(args.Query) == "" {
		return "", fmt.Errorf("query must not be empty")
	}

	count := webSearchDefaultCount
	if args.Count != nil && *args.Count > 0 {
		count = *args.Count
	}
	if count > webSearchMaxCount {
		count = webSearchMaxCount
	}

	provider := t.cfg.Provider
	if provider == "" {
		provider = "duckduckgo"
	}

	switch provider {
	case "duckduckgo":
		return t.searchDuckDuckGo(ctx, args.Query, count)
	case "google":
		return t.searchGoogle(ctx, args.Query, count)
	case "searxng":
		return t.searchSearxNG(ctx, args.Query, count)
	default:
		return "", fmt.Errorf("unsupported search provider: %s (expected duckduckgo, google, or searxng)", provider)
	}
}

// --- DuckDuckGo (JSON API, no key required) ---

func (t *webSearchTool) searchDuckDuckGo(ctx context.Context, query string, count int) (string, error) {
	baseURL := t.cfg.BaseURL
	if baseURL == "" {
		baseURL = duckDuckGoBaseURL
	}

	params := url.Values{
		"q":              {query},
		"format":         {"json"},
		"no_html":        {"1"},
		"no_redirect":    {"1"},
		"skip_disambig":  {"1"},
		"t":              {"yantra"},
	}
	reqURL := baseURL + "?" + params.Encode()

	body, err := t.doSearchRequest(ctx, reqURL)
	if err != nil {
		return "", err
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("duckduckgo returned invalid JSON: %w", err)
	}

	results := parseDDGResults(query, payload, count)
	return formatSearchResponse(query, "duckduckgo", baseURL, results), nil
}

// parseDDGResults extracts results from DuckDuckGo's Instant Answer API.
// It collects the Abstract (if present) and then RelatedTopics recursively.
func parseDDGResults(query string, payload map[string]any, count int) []searchResult {
	var results []searchResult
	seen := make(map[string]bool)

	// Primary result from Abstract.
	if abstractURL, _ := payload["AbstractURL"].(string); abstractURL != "" {
		seen[abstractURL] = true
		heading, _ := payload["Heading"].(string)
		if heading == "" {
			heading = query
		}
		snippet := ""
		if s, ok := payload["AbstractText"].(string); ok && s != "" {
			snippet = s
		} else if s, ok := payload["Abstract"].(string); ok {
			snippet = s
		}
		source, _ := payload["AbstractSource"].(string)
		results = append(results, searchResult{
			Title:      heading,
			URL:        abstractURL,
			DisplayURL: source,
			Snippet:    cleanSnippet(snippet),
		})
	}

	// Related topics (recursive).
	if topics, ok := payload["RelatedTopics"].([]any); ok {
		collectDDGTopics(topics, &results, seen, count)
	}

	// Cap to requested count and assign indices.
	if len(results) > count {
		results = results[:count]
	}
	for i := range results {
		results[i].Index = i + 1
	}
	return results
}

func collectDDGTopics(topics []any, results *[]searchResult, seen map[string]bool, count int) {
	if len(*results) >= count {
		return
	}
	for _, item := range topics {
		if len(*results) >= count {
			return
		}
		topic, ok := item.(map[string]any)
		if !ok {
			continue
		}

		// Nested topic groups.
		if nested, ok := topic["Topics"].([]any); ok {
			collectDDGTopics(nested, results, seen, count)
			continue
		}

		urlStr, _ := topic["FirstURL"].(string)
		if urlStr == "" || seen[urlStr] {
			continue
		}
		seen[urlStr] = true

		text, _ := topic["Text"].(string)
		text = strings.TrimSpace(text)
		title := text
		if idx := strings.Index(text, " - "); idx > 0 {
			title = strings.TrimSpace(text[:idx])
		}
		if title == "" {
			title = "DuckDuckGo result"
		}

		*results = append(*results, searchResult{
			Title:   title,
			URL:     urlStr,
			Snippet: cleanSnippet(text),
		})
	}
}

// --- Google Custom Search ---

func (t *webSearchTool) searchGoogle(ctx context.Context, query string, count int) (string, error) {
	apiKey := resolveEnvVar(t.cfg.APIKeyEnv)
	if apiKey == "" {
		return "", fmt.Errorf("google search requires an API key: set %s or configure tools.web_search.api_key_env", t.cfg.APIKeyEnv)
	}
	cx := resolveEnvVar(t.cfg.GoogleCXEnv)
	if cx == "" {
		return "", fmt.Errorf("google search requires a Custom Search engine ID: set %s or configure tools.web_search.google_cx_env", t.cfg.GoogleCXEnv)
	}

	baseURL := t.cfg.BaseURL
	if baseURL == "" {
		baseURL = googleBaseURL
	}

	params := url.Values{
		"key": {apiKey},
		"cx":  {cx},
		"q":   {query},
		"num": {fmt.Sprintf("%d", count)},
	}
	reqURL := baseURL + "?" + params.Encode()

	body, err := t.doSearchRequest(ctx, reqURL)
	if err != nil {
		return "", err
	}

	var payload struct {
		Items []struct {
			Title       string `json:"title"`
			Link        string `json:"link"`
			DisplayLink string `json:"displayLink"`
			Snippet     string `json:"snippet"`
			HTMLSnippet string `json:"htmlSnippet"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("google returned invalid JSON: %w", err)
	}

	var results []searchResult
	for i, item := range payload.Items {
		if i >= count {
			break
		}
		snippet := item.Snippet
		if snippet == "" {
			snippet = item.HTMLSnippet
		}
		results = append(results, searchResult{
			Index:      i + 1,
			Title:      item.Title,
			URL:        item.Link,
			DisplayURL: item.DisplayLink,
			Snippet:    cleanSnippet(snippet),
		})
	}

	return formatSearchResponse(query, "google", baseURL, results), nil
}

// --- SearxNG (self-hosted, JSON API) ---

func (t *webSearchTool) searchSearxNG(ctx context.Context, query string, count int) (string, error) {
	baseURL := t.cfg.BaseURL
	if baseURL == "" {
		return "", fmt.Errorf("searxng requires base_url in config (e.g., http://localhost:8080)")
	}
	baseURL = strings.TrimRight(baseURL, "/")

	params := url.Values{
		"q":      {query},
		"format": {"json"},
		"pageno": {"1"},
	}
	reqURL := baseURL + "/search?" + params.Encode()

	body, err := t.doSearchRequest(ctx, reqURL)
	if err != nil {
		return "", err
	}

	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("searxng returned invalid JSON: %w", err)
	}

	var results []searchResult
	for i, r := range payload.Results {
		if i >= count {
			break
		}
		results = append(results, searchResult{
			Index:   i + 1,
			Title:   r.Title,
			URL:     r.URL,
			Snippet: cleanSnippet(r.Content),
		})
	}

	return formatSearchResponse(query, "searxng", baseURL, results), nil
}

// --- Shared helpers ---

type searchResult struct {
	Index      int    `json:"index"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	DisplayURL string `json:"display_url,omitempty"`
	Snippet    string `json:"snippet"`
}

func (t *webSearchTool) doSearchRequest(ctx context.Context, reqURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Yantra/1.0 (AI Agent; +https://github.com/hackertron/Yantra)")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: webSearchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("search request returned %d: %s", resp.StatusCode, string(preview))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webSearchMaxBody))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	return body, nil
}

func formatSearchResponse(query, provider, baseURL string, results []searchResult) string {
	resp := struct {
		Query       string         `json:"query"`
		Provider    string         `json:"provider"`
		BaseURL     string         `json:"base_url"`
		ResultCount int            `json:"result_count"`
		Results     []searchResult `json:"results"`
	}{
		Query:       query,
		Provider:    provider,
		BaseURL:     baseURL,
		ResultCount: len(results),
		Results:     results,
	}

	b, _ := json.Marshal(resp)
	return string(b)
}

// cleanSnippet strips HTML tags, decodes entities, collapses whitespace, and truncates.
func cleanSnippet(s string) string {
	s = stripHTMLTags(s)
	s = decodeHTMLEntities(s)
	s = collapseWhitespace(s)
	if len(s) > webSearchMaxSnippetLen {
		s = s[:webSearchMaxSnippetLen] + "\n\n[truncated]"
	}
	return s
}

func stripHTMLTags(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func decodeHTMLEntities(s string) string {
	r := strings.NewReplacer(
		"&nbsp;", " ",
		"&amp;", "&",
		"&quot;", `"`,
		"&#39;", "'",
		"&#x27;", "'",
		"&lt;", "<",
		"&gt;", ">",
	)
	return r.Replace(s)
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func resolveEnvVar(envName string) string {
	if envName == "" {
		return ""
	}
	// Support "env:VAR_NAME" prefix convention.
	envName = strings.TrimPrefix(envName, "env:")
	return os.Getenv(envName)
}
