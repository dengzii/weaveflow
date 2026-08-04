package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/tmc/langchaingo/llms"
)

const (
	defaultFetchLimit = 64 * 1024
	maxFetchLimit     = 256 * 1024
	fetchTimeout      = 30 * time.Second
)

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
		Handler: webFetchTool,
	}
}

func webFetchTool(_ context.Context, input string) (string, error) {
	var req webFetchRequest
	if err := decodeToolRequest(input, "web_fetch", &req); err != nil {
		return "", err
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	req.Description = strings.TrimSpace(req.Description)
	if req.Description == "" {
		return "", fmt.Errorf("description is required")
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		req.URL = "https://" + req.URL
	}

	limit := normalizeFetchLimit(req.MaxBytes)

	client := &http.Client{Timeout: fetchTimeout}
	httpReq, err := http.NewRequest("GET", req.URL, nil)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 (compatible; WeaveFlow/1.0)")
	httpReq.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,*/*;q=0.7")

	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxFetchLimit+1024)))
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")

	var title, text string
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml") {
		title, text, err = htmlToText(body)
		if err != nil {
			text = string(body)
		}
	} else {
		text = string(body)
	}

	truncated := false
	if len(text) > limit {
		text = text[:limit]
		truncated = true
	}

	result := webFetchResponse{
		URL:       req.URL,
		Status:    resp.StatusCode,
		Title:     title,
		Content:   text,
		Truncated: truncated,
	}

	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
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
					sb.WriteString(fmt.Sprintf("[%s](%s)", linkText, href))
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
