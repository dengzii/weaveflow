package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	defaultSearchResultLimit = 10
	maxSearchResultLimit     = 50
	maxSearchResponseBytes   = 4 * 1024 * 1024
	searchTimeout            = 20 * time.Second
	tavilyAPIKeyEnvironment  = "TAVILY_API_KEY"
	braveAPIKeyEnvironment   = "BRAVE_API_KEY"

	duckDuckGoSearchURL = "https://lite.duckduckgo.com/lite/"
	bingSearchURL       = "https://www.bing.com/search"
	tavilySearchURL     = "https://api.tavily.com/search"
	braveSearchURL      = "https://api.search.brave.com/res/v1/web/search"
)

var searchUserAgents = [...]string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
}

var searchAcceptLanguages = [...]string{
	"en-US,en;q=0.9",
	"en-US,en;q=0.9,es;q=0.8",
	"en-GB,en;q=0.9,en-US;q=0.8",
	"en-US,en;q=0.5",
	"en-CA,en;q=0.9,en-US;q=0.8",
}

var duckDuckGoSearchPacer struct {
	sync.Mutex
	lastSearch time.Time
}

var defaultSearchHTTPClient = newSearchHTTPClient()

type searchResult struct {
	Title   string
	URL     string
	Snippet string
	Engine  string
}

type searchEngine interface {
	Name() string
	Search(context.Context, *http.Client, string, int) ([]searchResult, error)
}

type webSearcher struct {
	client  *http.Client
	engines []searchEngine
}

func newWebSearcher(tavilyAPIKey, braveAPIKey string) *webSearcher {
	engines := []searchEngine{
		duckDuckGoSearchEngine{endpoint: duckDuckGoSearchURL},
		bingSearchEngine{endpoint: bingSearchURL},
	}
	if apiKey := strings.TrimSpace(braveAPIKey); apiKey != "" {
		engines = append(engines, braveSearchEngine{endpoint: braveSearchURL, apiKey: apiKey})
	}
	if apiKey := strings.TrimSpace(tavilyAPIKey); apiKey != "" {
		engines = append(engines, tavilySearchEngine{endpoint: tavilySearchURL, apiKey: apiKey})
	}
	return &webSearcher{
		client:  defaultSearchHTTPClient,
		engines: engines,
	}
}

func newSearchHTTPClient() *http.Client {
	client := &http.Client{Timeout: searchTimeout}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return client
	}
	transport = transport.Clone()
	transport.MaxIdleConns = max(transport.MaxIdleConns, 100)
	transport.MaxIdleConnsPerHost = max(transport.MaxIdleConnsPerHost, 10)
	transport.IdleConnTimeout = 90 * time.Second
	client.Transport = transport
	return client
}

func (searcher *webSearcher) Search(ctx context.Context, query string, limit int) ([]searchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	limit = normalizeSearchLimit(limit)
	if searcher == nil || searcher.client == nil || len(searcher.engines) == 0 {
		return nil, fmt.Errorf("no search engines configured")
	}

	resultSets := make([][]searchResult, len(searcher.engines))
	searchErrors := make([]error, len(searcher.engines))
	var waitGroup sync.WaitGroup
	waitGroup.Add(len(searcher.engines))
	for index, engine := range searcher.engines {
		go func(index int, engine searchEngine) {
			defer waitGroup.Done()
			if engine == nil {
				searchErrors[index] = errors.New("search engine is nil")
				return
			}
			results, err := engine.Search(ctx, searcher.client, query, limit)
			resultSets[index] = results
			if err != nil {
				searchErrors[index] = fmt.Errorf("%s: %w", engine.Name(), err)
			}
		}(index, engine)
	}
	waitGroup.Wait()

	successfulEngines := 0
	var joinedErrors []error
	for _, err := range searchErrors {
		if err != nil {
			joinedErrors = append(joinedErrors, err)
			continue
		}
		successfulEngines++
	}
	if successfulEngines == 0 {
		return nil, errors.Join(joinedErrors...)
	}

	return mergeSearchResults(resultSets, limit), nil
}

func mergeSearchResults(resultSets [][]searchResult, limit int) []searchResult {
	limit = normalizeSearchLimit(limit)
	results := make([]searchResult, 0, limit)
	seen := make(map[string]struct{})
	for resultIndex := 0; len(results) < limit; resultIndex++ {
		hasCandidate := false
		for _, resultSet := range resultSets {
			if resultIndex >= len(resultSet) {
				continue
			}
			hasCandidate = true
			result := resultSet[resultIndex]
			result.Title = cleanSearchText(result.Title)
			result.URL = absoluteSearchResultURL(result.URL, "")
			result.Snippet = cleanSearchText(result.Snippet)
			result.Engine = strings.TrimSpace(result.Engine)
			if result.Title == "" || result.URL == "" {
				continue
			}
			key := searchResultKey(result.URL)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			results = append(results, result)
			if len(results) == limit {
				break
			}
		}
		if !hasCandidate {
			break
		}
	}
	return results
}

func searchResultKey(rawURL string) string {
	parsedURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsedURL.Host == "" {
		return strings.ToLower(strings.TrimSpace(rawURL))
	}
	parsedURL.Scheme = strings.ToLower(parsedURL.Scheme)
	parsedURL.Host = strings.ToLower(parsedURL.Host)
	parsedURL.Fragment = ""
	if parsedURL.Path == "/" {
		parsedURL.Path = ""
	}
	return parsedURL.String()
}

type duckDuckGoSearchEngine struct {
	endpoint string
}

func (engine duckDuckGoSearchEngine) Name() string {
	return "duckduckgo"
}

func (engine duckDuckGoSearchEngine) Search(ctx context.Context, client *http.Client, query string, limit int) ([]searchResult, error) {
	if isDuckDuckGoEndpoint(engine.endpoint) {
		if err := waitForDuckDuckGoSearch(ctx); err != nil {
			return nil, err
		}
	}
	request, err := newSearchRequest(ctx, engine.endpoint, query, 0)
	if err != nil {
		return nil, err
	}
	return executeHTMLSearch(client, request, limit, parseDuckDuckGoResults)
}

func isDuckDuckGoEndpoint(endpoint string) bool {
	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	hostname := strings.ToLower(parsedURL.Hostname())
	return hostname == "duckduckgo.com" || strings.HasSuffix(hostname, ".duckduckgo.com")
}

func waitForDuckDuckGoSearch(ctx context.Context) error {
	duckDuckGoSearchPacer.Lock()
	defer duckDuckGoSearchPacer.Unlock()

	minGap := time.Duration(500+rand.IntN(1500)) * time.Millisecond
	remaining := minGap - time.Since(duckDuckGoSearchPacer.lastSearch)
	if remaining > 0 {
		timer := time.NewTimer(remaining)
		defer func() { _ = timer.Stop() }()
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for search rate limit: %w", ctx.Err())
		case <-timer.C:
		}
	}
	duckDuckGoSearchPacer.lastSearch = time.Now()
	return nil
}

func parseDuckDuckGoResults(reader io.Reader, limit int) ([]searchResult, error) {
	limit = normalizeSearchLimit(limit)
	document, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return nil, fmt.Errorf("parse response html: %w", err)
	}
	if document.Find(".result").Length() == 0 {
		return parseDuckDuckGoLiteResults(document, limit), nil
	}
	results := make([]searchResult, 0, limit)
	document.Find(".result").EachWithBreak(func(_ int, selection *goquery.Selection) bool {
		if selection.HasClass("result--ad") {
			return true
		}
		link := selection.Find("a.result__a").First()
		rawURL, exists := link.Attr("href")
		if !exists {
			return true
		}
		resultURL := duckDuckGoResultURL(rawURL)
		title := cleanSearchText(link.Text())
		if resultURL == "" || title == "" {
			return true
		}
		results = append(results, searchResult{
			Title:   title,
			URL:     resultURL,
			Snippet: cleanSearchText(selection.Find(".result__snippet").First().Text()),
			Engine:  "duckduckgo",
		})
		return len(results) < limit
	})
	return results, nil
}

func parseDuckDuckGoLiteResults(document *goquery.Document, limit int) []searchResult {
	links := document.Find("a.result-link")
	snippets := document.Find("td.result-snippet, .result-snippet")
	results := make([]searchResult, 0, min(limit, links.Length()))
	links.EachWithBreak(func(index int, link *goquery.Selection) bool {
		rawURL, exists := link.Attr("href")
		if !exists {
			return true
		}
		resultURL := duckDuckGoResultURL(rawURL)
		title := cleanSearchText(link.Text())
		if resultURL == "" || title == "" {
			return true
		}
		snippet := ""
		if row := link.Closest("tr"); row.Length() > 0 {
			snippet = row.Find("td.result-snippet, .result-snippet").First().Text()
		}
		if snippet == "" && index < snippets.Length() {
			snippet = snippets.Eq(index).Text()
		}
		results = append(results, searchResult{
			Title:   title,
			URL:     resultURL,
			Snippet: cleanSearchText(snippet),
			Engine:  "duckduckgo",
		})
		return len(results) < limit
	})
	return results
}

func duckDuckGoResultURL(rawURL string) string {
	resultURL := absoluteSearchResultURL(rawURL, "https://duckduckgo.com")
	parsedURL, err := url.Parse(resultURL)
	if err != nil || parsedURL == nil {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(parsedURL.Hostname()), "duckduckgo.com") && parsedURL.Path == "/l/" {
		return absoluteSearchResultURL(parsedURL.Query().Get("uddg"), "")
	}
	return resultURL
}

type bingSearchEngine struct {
	endpoint string
}

func (engine bingSearchEngine) Name() string {
	return "bing"
}

func (engine bingSearchEngine) Search(ctx context.Context, client *http.Client, query string, limit int) ([]searchResult, error) {
	request, err := newSearchRequest(ctx, engine.endpoint, query, limit)
	if err != nil {
		return nil, err
	}
	return executeHTMLSearch(client, request, limit, parseBingResults)
}

func parseBingResults(reader io.Reader, limit int) ([]searchResult, error) {
	limit = normalizeSearchLimit(limit)
	document, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return nil, fmt.Errorf("parse response html: %w", err)
	}
	results := make([]searchResult, 0, limit)
	document.Find("li.b_algo").EachWithBreak(func(_ int, selection *goquery.Selection) bool {
		link := selection.Find("h2 a").First()
		rawURL, exists := link.Attr("href")
		if !exists {
			return true
		}
		resultURL := bingResultURL(rawURL)
		title := cleanSearchText(link.Text())
		if resultURL == "" || title == "" {
			return true
		}
		results = append(results, searchResult{
			Title:   title,
			URL:     resultURL,
			Snippet: cleanSearchText(selection.Find(".b_caption p, .b_snippet, .b_lineclamp2, .b_lineclamp3").First().Text()),
			Engine:  "bing",
		})
		return len(results) < limit
	})
	return results, nil
}

func bingResultURL(rawURL string) string {
	resultURL := absoluteSearchResultURL(rawURL, "https://www.bing.com")
	parsedURL, err := url.Parse(resultURL)
	if err != nil || parsedURL == nil {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(parsedURL.Hostname()), "bing.com") && strings.HasPrefix(parsedURL.Path, "/ck/") {
		if decodedURL := decodeBingResultURL(parsedURL.Query().Get("u")); decodedURL != "" {
			return decodedURL
		}
	}
	return resultURL
}

func decodeBingResultURL(value string) string {
	value = strings.TrimSpace(value)
	if resultURL := absoluteSearchResultURL(value, ""); resultURL != "" {
		return resultURL
	}
	if strings.HasPrefix(value, "a1") {
		value = strings.TrimPrefix(value, "a1")
	}
	encodings := []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(value)
		if err != nil {
			continue
		}
		if resultURL := absoluteSearchResultURL(string(decoded), ""); resultURL != "" {
			return resultURL
		}
	}
	return ""
}

type tavilySearchEngine struct {
	endpoint string
	apiKey   string
}

func (engine tavilySearchEngine) Name() string {
	return "tavily"
}

func (engine tavilySearchEngine) Search(ctx context.Context, client *http.Client, query string, limit int) ([]searchResult, error) {
	limit = normalizeSearchLimit(limit)
	body, err := json.Marshal(map[string]any{
		"query":        query,
		"api_key":      engine.apiKey,
		"search_depth": "basic",
		"max_results":  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, engine.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("unexpected status: %s", response.Status)
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	reader := io.LimitReader(response.Body, maxSearchResponseBytes)
	if err := json.NewDecoder(reader).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	results := make([]searchResult, 0, min(limit, len(payload.Results)))
	for _, result := range payload.Results {
		if len(results) == limit {
			break
		}
		results = append(results, searchResult{
			Title:   result.Title,
			URL:     result.URL,
			Snippet: result.Content,
			Engine:  "tavily",
		})
	}
	return results, nil
}

func newSearchRequest(ctx context.Context, endpoint, query string, limit int) (*http.Request, error) {
	searchURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	values := searchURL.Query()
	values.Set("q", query)
	if limit > 0 {
		values.Set("count", strconv.Itoa(limit))
	}
	searchURL.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("Accept-Language", searchAcceptLanguages[rand.IntN(len(searchAcceptLanguages))])
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("Connection", "keep-alive")
	request.Header.Set("Upgrade-Insecure-Requests", "1")
	request.Header.Set("Sec-Fetch-Dest", "document")
	request.Header.Set("Sec-Fetch-Mode", "navigate")
	request.Header.Set("Sec-Fetch-Site", "none")
	request.Header.Set("Sec-Fetch-User", "?1")
	request.Header.Set("Cache-Control", "max-age=0")
	request.Header.Set("DNT", "1")
	request.Header.Set("User-Agent", searchUserAgents[rand.IntN(len(searchUserAgents))])
	return request, nil
}

func executeHTMLSearch(client *http.Client, request *http.Request, limit int, parse func(io.Reader, int) ([]searchResult, error)) ([]searchResult, error) {
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("unexpected status: %s", response.Status)
	}
	return parse(io.LimitReader(response.Body, maxSearchResponseBytes), limit)
}

func absoluteSearchResultURL(rawURL, baseURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if !parsedURL.IsAbs() {
		if baseURL == "" {
			return ""
		}
		base, baseErr := url.Parse(baseURL)
		if baseErr != nil {
			return ""
		}
		parsedURL = base.ResolveReference(parsedURL)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return ""
	}
	return parsedURL.String()
}

type braveSearchEngine struct {
	endpoint string
	apiKey   string
}

func (engine braveSearchEngine) Name() string {
	return "brave"
}

func (engine braveSearchEngine) Search(ctx context.Context, client *http.Client, query string, limit int) ([]searchResult, error) {
	limit = normalizeSearchLimit(limit)
	requestURL, err := url.Parse(engine.endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	values := requestURL.Query()
	values.Set("q", query)
	values.Set("count", strconv.Itoa(limit))
	requestURL.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Subscription-Token", engine.apiKey)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("unexpected status: %s", response.Status)
	}
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	reader := io.LimitReader(response.Body, maxSearchResponseBytes)
	if err := json.NewDecoder(reader).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	results := make([]searchResult, 0, min(limit, len(payload.Web.Results)))
	for _, result := range payload.Web.Results {
		if len(results) == limit {
			break
		}
		results = append(results, searchResult{
			Title:   result.Title,
			URL:     result.URL,
			Snippet: result.Description,
			Engine:  "brave",
		})
	}
	return results, nil
}

func cleanSearchText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func normalizeSearchLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultSearchResultLimit
	case limit > maxSearchResultLimit:
		return maxSearchResultLimit
	default:
		return limit
	}
}
