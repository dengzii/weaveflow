package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dengzii/weaveflow/llms"
)

const (
	defaultFetchLimit = 64 * 1024
	maxFetchLimit     = 256 * 1024
	maxFetchBodyBytes = 4 * 1024 * 1024
	fetchTimeout      = 30 * time.Second
)

type webFetchPage struct {
	Status        int
	ContentType   string
	Body          []byte
	BodyTruncated bool
}

type webFetchPageLoader func(context.Context, string) (webFetchPage, error)

type webFetchPageFetcher struct {
	browser webFetchPageLoader
	http    webFetchPageLoader
}

var defaultWebFetchPageFetcher = webFetchPageFetcher{
	browser: fetchWebPageWithBrowser,
	http:    fetchWebPageWithHTTP,
}

type webFetchRequest struct {
	URL         string `json:"url"`
	Prompt      string `json:"prompt,omitempty"`
	Description string `json:"description"`
	MaxBytes    int    `json:"max_bytes,omitempty"`
}

type webFetchResponse struct {
	URL       string `json:"url"`
	Status    int    `json:"status"`
	Title     string `json:"title,omitempty"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

func NewWebFetch() Tool {
	return Tool{
		Function: &llms.FunctionDefinition{
			Name: "web_fetch",
			Description: "Fetches content from a specified URL and returns readable text content. " +
				"HTML is converted to plain text. Use this tool when you need to retrieve and analyze web content.",
			OutputSchema: objectOutputSchema(map[string]any{
				"url":       map[string]any{"type": "string"},
				"status":    map[string]any{"type": "integer"},
				"title":     map[string]any{"type": "string"},
				"content":   map[string]any{"type": "string"},
				"truncated": map[string]any{"type": "boolean"},
			}, "url", "status", "content"),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"format":      "uri",
						"description": "The URL to fetch content from",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "The prompt to run on the fetched content",
					},
					"description": map[string]any{
						"type":        "string",
						"minLength":   1,
						"description": "Clear, concise description of why this URL is being fetched.",
					},
					"max_bytes": map[string]any{
						"type":        "integer",
						"description": "Optional max bytes of text content to return. Default 64KB, max 256KB.",
					},
				},
				"required":             []string{"url", "prompt", "description"},
				"additionalProperties": false,
			},
		},
		Handler:     webFetchTool,
		Effect:      EffectReadOnly,
		Permissions: []string{"network.http"},
	}
}

func webFetchTool(ctx context.Context, call llms.ToolCall) (llms.ToolResult, error) {
	return webFetchToolWithFetcher(ctx, call, defaultWebFetchPageFetcher)
}

func webFetchToolWithFetcher(ctx context.Context, call llms.ToolCall, fetcher webFetchPageFetcher) (llms.ToolResult, error) {
	var req webFetchRequest
	if err := decodeToolArguments(call, &req); err != nil {
		return llms.ToolResult{}, fmt.Errorf("web_fetch input: %w", err)
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		return llms.ToolResult{}, fmt.Errorf("url is required")
	}
	req.Description = strings.TrimSpace(req.Description)
	if req.Description == "" {
		return llms.ToolResult{}, fmt.Errorf("description is required")
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		req.URL = "https://" + req.URL
	}
	if _, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil); err != nil {
		return llms.ToolResult{}, fmt.Errorf("invalid url: %w", err)
	}

	limit := normalizeFetchLimit(req.MaxBytes)

	fetchCtx, cancelFetch := context.WithTimeout(ctx, fetchTimeout)
	defer cancelFetch()
	page, err := fetcher.fetch(fetchCtx, req.URL)
	if err != nil {
		return llms.ToolResult{}, fmt.Errorf("fetch failed: %w", err)
	}

	var title, text string
	if strings.Contains(page.ContentType, "text/html") || strings.Contains(page.ContentType, "application/xhtml") {
		title, text, err = htmlToText(page.Body)
		if err != nil {
			text = string(page.Body)
		}
	} else {
		text = string(page.Body)
	}

	truncated := page.BodyTruncated
	if len(text) > limit {
		text = text[:limit]
		truncated = true
	}

	result := webFetchResponse{
		URL:       req.URL,
		Status:    page.Status,
		Title:     title,
		Content:   text,
		Truncated: truncated,
	}

	return structuredToolResult(call, result)
}

func (fetcher webFetchPageFetcher) fetch(ctx context.Context, rawURL string) (webFetchPage, error) {
	var browserErr error
	if fetcher.browser != nil {
		page, err := fetcher.browser(ctx, rawURL)
		if err == nil {
			return page, nil
		}
		browserErr = fmt.Errorf("browser: %w", err)
		if ctx.Err() != nil {
			return webFetchPage{}, fmt.Errorf("browser: %w", ctx.Err())
		}
	}

	if fetcher.http == nil {
		if browserErr != nil {
			return webFetchPage{}, browserErr
		}
		return webFetchPage{}, fmt.Errorf("no web fetch method is configured")
	}
	page, err := fetcher.http(ctx, rawURL)
	if err != nil {
		httpErr := fmt.Errorf("http: %w", err)
		if browserErr != nil {
			return webFetchPage{}, errors.Join(browserErr, httpErr)
		}
		return webFetchPage{}, httpErr
	}
	return page, nil
}

func fetchWebPageWithHTTP(ctx context.Context, rawURL string) (webFetchPage, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return webFetchPage{}, fmt.Errorf("create request: %w", err)
	}
	setBrowserNavigationHeaders(request, "")

	client := &http.Client{Timeout: fetchTimeout}
	response, err := client.Do(request)
	if err != nil {
		return webFetchPage{}, fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(maxFetchBodyBytes+1)))
	if err != nil {
		return webFetchPage{}, fmt.Errorf("read response: %w", err)
	}
	return newWebFetchPage(response.StatusCode, response.Header.Get("Content-Type"), body), nil
}

func newWebFetchPage(status int, contentType string, body []byte) webFetchPage {
	bodyTruncated := len(body) > maxFetchBodyBytes
	if bodyTruncated {
		body = body[:maxFetchBodyBytes]
	}
	return webFetchPage{
		Status:        status,
		ContentType:   contentType,
		Body:          body,
		BodyTruncated: bodyTruncated,
	}
}

func htmlToText(raw []byte) (title string, text string, err error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(raw)))
	if err != nil {
		return "", "", err
	}

	title = strings.TrimSpace(doc.Find("title").First().Text())

	doc.Find("script, style, noscript, iframe, svg, head").Remove()

	var sb strings.Builder
	var extract func(*goquery.Selection)
	extract = func(s *goquery.Selection) {
		s.Contents().Each(func(_ int, child *goquery.Selection) {
			if goquery.NodeName(child) == "#text" {
				t := strings.TrimSpace(child.Text())
				if t != "" {
					sb.WriteString(t)
					sb.WriteByte(' ')
				}
				return
			}

			tag := goquery.NodeName(child)
			isBlock := isBlockElement(tag)
			if isBlock && sb.Len() > 0 {
				sb.WriteByte('\n')
			}

			if tag == "a" {
				linkText := strings.TrimSpace(child.Text())
				href, exists := child.Attr("href")
				if exists && linkText != "" {
					fmt.Fprintf(&sb, "[%s](%s)", linkText, href)
					sb.WriteByte(' ')
					return
				}
			}

			extract(child)

			if isBlock {
				sb.WriteByte('\n')
			}
		})
	}

	extract(doc.Find("body"))

	lines := strings.Split(sb.String(), "\n")
	var cleaned []string
	prevEmpty := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if !prevEmpty {
				cleaned = append(cleaned, "")
				prevEmpty = true
			}
			continue
		}
		cleaned = append(cleaned, line)
		prevEmpty = false
	}

	return title, strings.TrimSpace(strings.Join(cleaned, "\n")), nil
}

func isBlockElement(tag string) bool {
	switch tag {
	case "div", "p", "br", "h1", "h2", "h3", "h4", "h5", "h6",
		"ul", "ol", "li", "blockquote", "pre", "table", "tr",
		"section", "article", "header", "footer", "nav", "main",
		"figure", "figcaption", "details", "summary", "hr":
		return true
	}
	return false
}

func normalizeFetchLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultFetchLimit
	case limit > maxFetchLimit:
		return maxFetchLimit
	default:
		return limit
	}
}
