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

var webFetchSpec = json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","format":"uri","pattern":"^https?://"}},"required":["url"],"additionalProperties":false}`)

type webFetchTool struct{ guard *Guard }

// NewWebFetch creates the web_fetch tool.
func NewWebFetch(guard *Guard) core.Tool { return &webFetchTool{guard: guard} }

func (t *webFetchTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "web_fetch", Description: "Fetches a URL and returns its content as text.", InputSchema: webFetchSpec}
}
func (t *webFetchTool) Risk(json.RawMessage) core.Risk { return core.RiskNetwork }

func (t *webFetchTool) Call(ctx context.Context, tc core.ToolCtx, raw json.RawMessage) (core.ToolOutput, error) {
	var in struct {
		URL string `json:"url"`
	}
	if err := decodeInput(raw, &in); err != nil {
		return inputErrorOutput(err)
	}
	parsed, err := url.Parse(in.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errorOutput("url must be http or https")
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return errorOutput(err.Error())
	}
	resp, err := client.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return errorOutput("timed out after 30 seconds")
		}
		return errorOutput(err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return errorOutput(fmt.Sprintf("HTTP %d", resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchLimit+1))
	if err != nil {
		return errorOutput(err.Error())
	}
	if len(body) > webFetchLimit {
		return errorOutput("response too large")
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
			return errorOutput(err.Error())
		}
		md, err := htmltomarkdown.ConvertString(cleaned)
		if err != nil {
			return errorOutput(err.Error())
		}
		text = md
	case "text/plain", "application/json":
		text = string(body)
	default:
		return errorOutput("unsupported content type: " + mediaType)
	}
	return successOutput(truncateText(strings.TrimSpace(text), 30000))
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
