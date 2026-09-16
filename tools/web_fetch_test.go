package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebFetchToolConvertsHTMLAndReturnsStructuredMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		userAgent := request.Header.Get("User-Agent")
		if !strings.Contains(userAgent, "Mozilla/5.0") || strings.Contains(userAgent, "WeaveFlow") || !strings.Contains(request.Header.Get("Accept"), "text/html") {
			t.Errorf("request headers = %#v", request.Header)
		}
		for _, header := range []string{"Accept-Language", "Sec-Fetch-Dest", "Sec-Fetch-Mode", "Sec-Fetch-Site", "Sec-Fetch-User", "Upgrade-Insecure-Requests"} {
			if request.Header.Get(header) == "" {
				t.Errorf("request header %q is empty", header)
			}
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.WriteHeader(http.StatusAccepted)
		_, _ = fmt.Fprint(writer, `<html><head><title> Example </title><script>hidden()</script></head><body><main><h1>Hello</h1><p>Read <a href="/docs">the docs</a>.</p></main></body></html>`)
	}))
	defer server.Close()

	arguments := fmt.Sprintf(`{"url":%q,"description":"test page","prompt":"summarize"}`, server.URL)
	result, err := webFetchToolWithFetcher(context.Background(), toolCallForTest("web_fetch", arguments), httpOnlyWebFetchPageFetcher())
	if err != nil {
		t.Fatalf("webFetchTool() error = %v", err)
	}
	response, ok := result.Value.(webFetchResponse)
	if !ok {
		t.Fatalf("web fetch result = %#v", result.Value)
	}
	if response.URL != server.URL || response.Status != http.StatusAccepted || response.Title != "Example" || response.Truncated {
		t.Fatalf("web fetch metadata = %#v", response)
	}
	if !strings.Contains(response.Content, "Hello") || !strings.Contains(response.Content, "[the docs](/docs)") || strings.Contains(response.Content, "hidden") {
		t.Fatalf("web fetch content = %q", response.Content)
	}
}

func TestWebFetchToolReadsLargeHTMLBeforeExtractingText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(writer, "<html><body><p>start</p><!--%s--><p>end marker</p></body></html>", strings.Repeat("x", maxFetchLimit+1024))
	}))
	defer server.Close()

	arguments := fmt.Sprintf(`{"url":%q,"description":"large page","prompt":"read"}`, server.URL)
	result, err := webFetchToolWithFetcher(context.Background(), toolCallForTest("web_fetch", arguments), httpOnlyWebFetchPageFetcher())
	if err != nil {
		t.Fatalf("webFetchTool() error = %v", err)
	}
	response := result.Value.(webFetchResponse)
	if !strings.Contains(response.Content, "end marker") || response.Truncated {
		t.Fatalf("large HTML response = %#v", response)
	}
}

func TestWebFetchPageFetcherPrefersBrowserAndFallsBackToHTTP(t *testing.T) {
	browserPage := newWebFetchPage(http.StatusOK, "text/html", []byte("browser"))
	httpCalled := false
	fetcher := webFetchPageFetcher{
		browser: func(context.Context, string) (webFetchPage, error) {
			return browserPage, nil
		},
		http: func(context.Context, string) (webFetchPage, error) {
			httpCalled = true
			return newWebFetchPage(http.StatusOK, "text/plain", []byte("http")), nil
		},
	}
	page, err := fetcher.fetch(context.Background(), "https://example.com")
	if err != nil || string(page.Body) != "browser" || httpCalled {
		t.Fatalf("browser fetch = %#v, httpCalled = %t, error = %v", page, httpCalled, err)
	}

	fetcher.browser = func(context.Context, string) (webFetchPage, error) {
		return webFetchPage{}, errors.New("Chrome is unavailable")
	}
	page, err = fetcher.fetch(context.Background(), "https://example.com")
	if err != nil || string(page.Body) != "http" || !httpCalled {
		t.Fatalf("fallback fetch = %#v, httpCalled = %t, error = %v", page, httpCalled, err)
	}
}

func TestWebFetchPageFetcherDoesNotFallbackAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	httpCalled := false
	fetcher := webFetchPageFetcher{
		browser: func(ctx context.Context, _ string) (webFetchPage, error) {
			return webFetchPage{}, ctx.Err()
		},
		http: func(context.Context, string) (webFetchPage, error) {
			httpCalled = true
			return webFetchPage{}, nil
		},
	}
	_, err := fetcher.fetch(ctx, "https://example.com")
	if !errors.Is(err, context.Canceled) || httpCalled {
		t.Fatalf("fetch error = %v, httpCalled = %t", err, httpCalled)
	}
}

func TestWebFetchToolHandlesPlainTextTruncationAndValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(writer, "abcdefghijklmnopqrstuvwxyz")
	}))
	defer server.Close()

	arguments := fmt.Sprintf(`{"url":%q,"description":"plain text","prompt":"read","max_bytes":8}`, server.URL)
	result, err := webFetchToolWithFetcher(context.Background(), toolCallForTest("web_fetch", arguments), httpOnlyWebFetchPageFetcher())
	if err != nil {
		t.Fatalf("webFetchTool() error = %v", err)
	}
	response := result.Value.(webFetchResponse)
	if response.Content != "abcdefgh" || !response.Truncated || response.Title != "" {
		t.Fatalf("plain text response = %#v", response)
	}

	for _, testCase := range []struct {
		arguments string
		contains  string
	}{
		{arguments: `{}`, contains: "url is required"},
		{arguments: `{"url":"example.com","description":" "}`, contains: "description is required"},
		{arguments: `{"url":"http://[::1","description":"invalid"}`, contains: "invalid url"},
	} {
		if _, err := webFetchToolWithFetcher(context.Background(), toolCallForTest("web_fetch", testCase.arguments), httpOnlyWebFetchPageFetcher()); err == nil || !strings.Contains(err.Error(), testCase.contains) {
			t.Fatalf("webFetchTool(%s) error = %v, want %q", testCase.arguments, err, testCase.contains)
		}
	}
	for input, want := range map[int]int{-1: defaultFetchLimit, 0: defaultFetchLimit, 10: 10, maxFetchLimit + 1: maxFetchLimit} {
		if got := normalizeFetchLimit(input); got != want {
			t.Fatalf("normalizeFetchLimit(%d) = %d, want %d", input, got, want)
		}
	}
	if !isBlockElement("section") || isBlockElement("span") {
		t.Fatal("isBlockElement() classification is incorrect")
	}
}

func httpOnlyWebFetchPageFetcher() webFetchPageFetcher {
	return webFetchPageFetcher{http: fetchWebPageWithHTTP}
}

func TestHTMLToTextHandlesMalformedAndEmptyDocuments(t *testing.T) {
	title, text, err := htmlToText([]byte(`<html><head><title>T</title></head><body><div>one<br>two</div><style>hidden</style></body></html>`))
	if err != nil || title != "T" || !strings.Contains(text, "one") || !strings.Contains(text, "two") || strings.Contains(text, "hidden") {
		t.Fatalf("htmlToText() = %q, %q, %v", title, text, err)
	}
	title, text, err = htmlToText(nil)
	if err != nil || title != "" || text != "" {
		t.Fatalf("htmlToText(nil) = %q, %q, %v", title, text, err)
	}
}
