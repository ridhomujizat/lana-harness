package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/ridhomujizat/lana-harness/core"
	"golang.org/x/net/html"
)

const webFetchLimit = 5 << 20

var webFetchTimeout = 30 * time.Second

var webFetchSpec = json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","format":"uri","pattern":"^https?://","description":"HTTP or HTTPS URL to fetch."}},"required":["url"],"additionalProperties":false}`)

// webFetchDescription explains what web_fetch returns and its main limits.
const webFetchDescription = "Fetches an HTTP or HTTPS URL and returns its content as text: HTML is converted to Markdown, and text/plain or application/json is returned raw. " +
	"Times out after 30 seconds, rejects responses over 5 MiB and non-text content types, and follows at most 5 redirects."

type webFetchTool struct{ guard *Guard }

// NewWebFetch creates the web_fetch tool.
func NewWebFetch(guard *Guard) core.Tool { return &webFetchTool{guard: guard} }

func (t *webFetchTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "web_fetch", Description: webFetchDescription, InputSchema: webFetchSpec}
}
func (t *webFetchTool) Risk(json.RawMessage) core.Risk { return core.RiskNetwork }

func (t *webFetchTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		URL string `json:"url"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	_, text, err := FetchMarkdown(ctx, in.URL)
	if err != nil {
		return errorOutput(err.Error())
	}
	return successOutput(text)
}

// FetchMarkdown fetches an HTTP or HTTPS URL and returns the page title (when
// the response is HTML) and the content converted to Markdown. It is the shared
// fetch-and-convert code of web_fetch and knowledge_ingest (REQ-KNW-020). The
// error texts are the ones web_fetch has always returned.
func FetchMarkdown(ctx context.Context, rawURL string) (title, markdown string, err error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", "", errors.New("url must be http or https")
	}
	client := &http.Client{
		Timeout: webFetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return "", "", errors.New("timed out after 30 seconds")
		}
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchLimit+1))
	if err != nil {
		return "", "", err
	}
	if len(body) > webFetchLimit {
		return "", "", errors.New("response too large")
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		mediaType = resp.Header.Get("Content-Type")
	}
	var text string
	switch mediaType {
	case "text/html":
		cleaned, err := stripHTML(body)
		if err != nil {
			return "", "", err
		}
		title = htmlTitle(cleaned)
		md, err := htmltomarkdown.ConvertString(cleaned)
		if err != nil {
			return "", "", err
		}
		text = md
	case "text/plain", "application/json":
		text = string(body)
	default:
		return "", "", errors.New("unsupported content type: " + mediaType)
	}
	return title, truncateText(strings.TrimSpace(text), maxToolOutput), nil
}

// htmlTitle returns the trimmed text of the first <title> element of a document.
func htmlTitle(doc string) string {
	node, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return ""
	}
	var title string
	var find func(*html.Node) bool
	find = func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "title" {
			title = strings.TrimSpace(textContent(n))
			return true
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if find(child) {
				return true
			}
		}
		return false
	}
	find(node)
	return title
}

// textContent concatenates the text nodes under n.
func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return b.String()
}

func stripHTML(body []byte) (string, error) {
	node, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript") {
			if n.Parent != nil {
				n.Parent.RemoveChild(n)
			}
			return
		}
		for child := n.FirstChild; child != nil; {
			next := child.NextSibling
			walk(child)
			child = next
		}
	}
	walk(node)
	var buf bytes.Buffer
	if err := html.Render(&buf, node); err != nil {
		return "", err
	}
	return buf.String(), nil
}
