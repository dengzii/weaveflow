package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dengzii/weaveflow/core"
)

const liveWebToolsTestEnvironment = "WEAVEFLOW_LIVE_WEB_TEST"

func TestWebSearchThenWebFetchFunctionalFlow(t *testing.T) {
	pageServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("fetch method = %s, want GET", request.Method)
		}
		if request.Header.Get("User-Agent") == "" || !strings.Contains(request.Header.Get("Accept"), "text/html") {
			t.Errorf("fetch headers = %#v", request.Header)
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(writer, `<html><head><title>WeaveFlow Guide</title><script>ignored()</script></head><body><main><h1>Build reliable graphs</h1><p>Read the <a href="/reference">reference</a>.</p></main></body></html>`)
	}))
	defer pageServer.Close()

	previousSearchClient := defaultSearchHTTPClient
	defaultSearchHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Hostname() {
		case "lite.duckduckgo.com":
			if request.Method != http.MethodGet || request.URL.Query().Get("q") != "weaveflow graph guide" {
				t.Errorf("search request = %s %s", request.Method, request.URL)
			}
			if request.Header.Get("User-Agent") == "" || request.Header.Get("Accept-Language") == "" {
				t.Errorf("search headers = %#v", request.Header)
			}
			body := fmt.Sprintf(`<html><body><table><tr><td><a class="result-link" href="%s">WeaveFlow guide</a></td></tr><tr><td class="result-snippet">Graph runtime documentation</td></tr></table></body></html>`, pageServer.URL)
			return searchFixtureResponse(request, http.StatusOK, body), nil
		case "www.bing.com":
			return searchFixtureResponse(request, http.StatusBadGateway, "temporary failure"), nil
		default:
			return nil, fmt.Errorf("unexpected search host %q", request.URL.Hostname())
		}
	})}
	t.Cleanup(func() {
		defaultSearchHTTPClient = previousSearchClient
	})

	ctx := core.WithToolPermissions(context.Background(), "network.search", "network.http")
	searchResult, err := core.ExecuteTool(ctx, NewWebSearch(), toolCallForTest("web_search", `{"query":"weaveflow graph guide"}`))
	if err != nil {
		t.Fatalf("execute web_search: %v", err)
	}
	if searchResult.Name != "web_search" || searchResult.ToolCallID != "test-call" {
		t.Fatalf("web_search metadata = %#v", searchResult)
	}
	if !strings.Contains(searchResult.Content, "Source: duckduckgo") || !strings.Contains(searchResult.Content, "Title: WeaveFlow guide") || !strings.Contains(searchResult.Content, "Content: Graph runtime documentation") {
		t.Fatalf("web_search content = %q", searchResult.Content)
	}
	resultURL := searchResultURL(searchResult.Content)
	if resultURL != pageServer.URL {
		t.Fatalf("web_search URL = %q, want %q", resultURL, pageServer.URL)
	}

	fetchArguments := fmt.Sprintf(`{"url":%q,"prompt":"extract the guide","description":"Read the search result"}`, resultURL)
	fetchResult, err := core.ExecuteTool(ctx, NewWebFetch(), toolCallForTest("web_fetch", fetchArguments))
	if err != nil {
		t.Fatalf("execute web_fetch: %v", err)
	}
	fetchResponse, ok := fetchResult.Value.(webFetchResponse)
	if !ok {
		t.Fatalf("web_fetch value = %#v", fetchResult.Value)
	}
	if fetchResponse.URL != pageServer.URL || fetchResponse.Status != http.StatusOK || fetchResponse.Title != "WeaveFlow Guide" || fetchResponse.Truncated {
		t.Fatalf("web_fetch metadata = %#v", fetchResponse)
	}
	if !strings.Contains(fetchResponse.Content, "Build reliable graphs") || !strings.Contains(fetchResponse.Content, "[reference](/reference)") || strings.Contains(fetchResponse.Content, "ignored") {
		t.Fatalf("web_fetch content = %q", fetchResponse.Content)
	}
}

func TestWebToolsLiveSmoke(t *testing.T) {
	if os.Getenv(liveWebToolsTestEnvironment) != "1" {
		t.Skipf("set %s=1 to run live web tool checks", liveWebToolsTestEnvironment)
	}

	ctx := core.WithToolPermissions(context.Background(), "network.search", "network.http")
	searchResult, err := core.ExecuteTool(ctx, NewWebSearch(), toolCallForTest("web_search", `{"query":"Go programming language official website"}`))
	if err != nil {
		t.Fatalf("execute live web_search: %v", err)
	}
	if searchResultURL(searchResult.Content) == "" {
		t.Fatalf("live web_search returned no usable URL: %q", searchResult.Content)
	}

	fetchResult, err := core.ExecuteTool(ctx, NewWebFetch(), toolCallForTest("web_fetch", `{"url":"https://example.com","prompt":"extract the page text","description":"Verify live web fetching"}`))
	if err != nil {
		t.Fatalf("execute live web_fetch: %v", err)
	}
	fetchResponse, ok := fetchResult.Value.(webFetchResponse)
	if !ok || fetchResponse.Status != http.StatusOK || !strings.Contains(fetchResponse.Content, "Example Domain") {
		t.Fatalf("live web_fetch response = %#v", fetchResult)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func searchFixtureResponse(request *http.Request, statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func searchResultURL(content string) string {
	for line := range strings.Lines(content) {
		if resultURL, found := strings.CutPrefix(strings.TrimSpace(line), "URL: "); found {
			return strings.TrimSpace(resultURL)
		}
	}
	return ""
}
