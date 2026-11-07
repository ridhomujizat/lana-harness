package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"golang.org/x/net/html"
)

const (
	webSearchDefaultCount = 8
	webSearchMinCount     = 1
	webSearchMaxCount     = 20
	webSearchLimit        = 5 << 20
	webSearchErrorBody    = 300
)

var (
	// webSearchBaseURL is a variable so tests can point it at an httptest server.
	webSearchBaseURL = "https://api.search.brave.com"
	webSearchTimeout = 20 * time.Second
)

var webSearchSpec = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","minLength":1,"description":"Words to search the web for."},"count":{"type":"integer","minimum":1,"maximum":20,"default":8,"description":"Number of results to return, 1-20. Defaults to 8."},"freshness":{"type":"string","enum":["pd","pw","pm","py"],"description":"Only results from the past day (pd), week (pw), month (pm), or year (py). Optional."},"country":{"type":"string","pattern":"^[A-Za-z]{2}$","description":"Two-letter country code to localize results, for example US. Optional."}},"required":["query"],"additionalProperties":false}`)

// webSearchDescription explains what web_search returns and how it fits with
// web_fetch.
const webSearchDescription = "Searches the web with the Brave Search API and returns the title, URL, and description of each result. " +
	"Use web_search to find pages, then web_fetch to read one."

type webSearchTool struct{}

// NewWebSearch creates the web_search tool.
func NewWebSearch() core.Tool { return &webSearchTool{} }

func (t *webSearchTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "web_search", Description: webSearchDescription, InputSchema: webSearchSpec}
}
func (t *webSearchTool) Risk(json.RawMessage) core.Risk { return core.RiskNetwork }

type webSearchResponse struct {
	Web struct {
		Results []webSearchResult `json:"results"`
	} `json:"web"`
}

type webSearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Age         string `json:"age"`
}

func (t *webSearchTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		Query     string `json:"query"`
		Count     *int   `json:"count"`
		Freshness string `json:"freshness"`
		Country   string `json:"country"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return inputErrorOutput(errors.New("query is required"))
	}
	count := webSearchDefaultCount
	if in.Count != nil {
		count = *in.Count
	}
	if count < webSearchMinCount || count > webSearchMaxCount {
		return inputErrorOutput(fmt.Errorf("count must be between %d and %d", webSearchMinCount, webSearchMaxCount))
	}
	switch in.Freshness {
	case "", "pd", "pw", "pm", "py":
	default:
		return inputErrorOutput(errors.New("freshness must be pd, pw, pm, or py"))
	}
	if in.Country != "" && (len(in.Country) != 2 || !isASCIILetters(in.Country)) {
		return inputErrorOutput(errors.New("country must be a two-letter code"))
	}
	key := strings.TrimSpace(os.Getenv("BRAVE_API_KEY"))
	if key == "" {
		return errorOutput("web_search is not configured: set BRAVE_API_KEY in the daemon environment")
	}

	endpoint, err := url.Parse(strings.TrimRight(webSearchBaseURL, "/") + "/res/v1/web/search")
	if err != nil {
		return errorOutput("invalid Brave base URL: " + err.Error())
	}
	params := endpoint.Query()
	params.Set("q", query)
	params.Set("count", strconv.Itoa(count))
	if in.Freshness != "" {
		params.Set("freshness", in.Freshness)
	}
	if in.Country != "" {
		params.Set("country", in.Country)
	}
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return errorOutput(err.Error())
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", key)

	client := &http.Client{Timeout: webSearchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return errorOutput(err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errorOutput("Brave rejected the API key")
	case resp.StatusCode == http.StatusTooManyRequests:
		return errorOutput("Brave rate limit reached, retry later")
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, webSearchErrorBody))
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			return errorOutput(fmt.Sprintf("Brave returned HTTP %d", resp.StatusCode))
		}
		return errorOutput(fmt.Sprintf("Brave returned HTTP %d: %s", resp.StatusCode, detail))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webSearchLimit+1))
	if err != nil {
		return errorOutput(err.Error())
	}
	if len(body) > webSearchLimit {
		return errorOutput("Brave response too large")
	}
	var result webSearchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return errorOutput("invalid Brave response: " + err.Error())
	}
	if len(result.Web.Results) == 0 {
		return successOutput("No results for " + query)
	}
	return successOutput(truncateText(formatWebSearchResults(result.Web.Results), maxToolOutput))
}

// isASCIILetters reports whether s contains only ASCII letters.
func isASCIILetters(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// formatWebSearchResults renders results as a numbered plain-text list.
func formatWebSearchResults(results []webSearchResult) string {
	var b strings.Builder
	for i, result := range results {
		title := htmlToText(result.Title)
		if title == "" {
			title = result.URL
		}
		fmt.Fprintf(&b, "%d. %s\n   %s", i+1, title, result.URL)
		description := htmlToText(result.Description)
		age := htmlToText(result.Age)
		switch {
		case description != "" && age != "":
			fmt.Fprintf(&b, "\n   %s (%s)", description, age)
		case description != "":
			fmt.Fprintf(&b, "\n   %s", description)
		case age != "":
			fmt.Fprintf(&b, "\n   (%s)", age)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// htmlToText strips HTML tags and unescapes HTML entities. Brave returns
// descriptions with <strong> around the matched words; the parser drops the
// tags and decodes entities in the text nodes.
func htmlToText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return strings.TrimSpace(html.UnescapeString(s))
	}
	return strings.TrimSpace(textContent(doc))
}
