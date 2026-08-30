package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestParseDuckDuckGoResults(t *testing.T) {
	encodedURL := url.QueryEscape("https://example.com/docs#section")
	html := fmt.Sprintf(`<html><body>
		<div class="result"><a class="result__a" href="/l/?uddg=%s"> First result </a><a class="result__snippet"> First snippet </a></div>
		<div class="result result--ad"><a class="result__a" href="https://ads.example.com">Ad</a></div>
		<div class="result"><a class="result__a" href="https://example.org/second">Second</a><div class="result__snippet">Second snippet</div></div>
	</body></html>`, encodedURL)

	results, err := parseDuckDuckGoResults(strings.NewReader(html), 10)
	if err != nil {
		t.Fatalf("parseDuckDuckGoResults() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v, want two results", results)
	}
	if results[0].Title != "First result" || results[0].URL != "https://example.com/docs#section" || results[0].Snippet != "First snippet" || results[0].Engine != "duckduckgo" {
		t.Fatalf("first result = %#v", results[0])
	}
}

func TestParseDuckDuckGoLiteResults(t *testing.T) {
	encodedURL := url.QueryEscape("https://example.com/lite")
	html := fmt.Sprintf(`<html><body><table>
		<tr><td><a class="result-link" href="//duckduckgo.com/l/?uddg=%s&amp;rut=ignored"> Lite result </a></td></tr>
		<tr><td class="result-snippet"> Lite <b>snippet</b> </td></tr>
	</table></body></html>`, encodedURL)

	results, err := parseDuckDuckGoResults(strings.NewReader(html), 10)
	if err != nil {
		t.Fatalf("parseDuckDuckGoResults() error = %v", err)
	}
	if len(results) != 1 || results[0].Title != "Lite result" || results[0].URL != "https://example.com/lite" || results[0].Snippet != "Lite snippet" || results[0].Engine != "duckduckgo" {
		t.Fatalf("results = %#v", results)
	}
}

func TestNewSearchRequestUsesBrowserHeaders(t *testing.T) {
	request, err := newSearchRequest(context.Background(), duckDuckGoSearchURL, "weave flow", 7)
	if err != nil {
		t.Fatalf("newSearchRequest() error = %v", err)
	}
	if request.URL.Query().Get("q") != "weave flow" || request.URL.Query().Get("count") != "7" {
		t.Fatalf("request URL = %s", request.URL)
	}
	for _, header := range []string{"User-Agent", "Accept", "Accept-Language", "Accept-Encoding", "Sec-Fetch-Mode"} {
		if request.Header.Get(header) == "" {
			t.Errorf("header %q is empty", header)
		}
	}
}

func TestParseBingResultsDecodesRedirectURL(t *testing.T) {
	redirectURL := "https://example.com/article"
	encodedRedirect := base64RawURL(redirectURL)
	html := fmt.Sprintf(`<html><body><ol><li class="b_algo"><h2><a href="/ck/a?u=a1%s">Bing title</a></h2><div class="b_caption"><p>Bing snippet</p></div></li></ol></body></html>`, encodedRedirect)

	results, err := parseBingResults(strings.NewReader(html), 10)
	if err != nil {
		t.Fatalf("parseBingResults() error = %v", err)
	}
	if len(results) != 1 || results[0].URL != redirectURL || results[0].Title != "Bing title" || results[0].Snippet != "Bing snippet" || results[0].Engine != "bing" {
		t.Fatalf("results = %#v", results)
	}
}

func TestWebSearcherMergesResultsAndDegradesOnEngineFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/duck" {
			writer.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(writer, `<div class="result"><a class="result__a" href="https://example.com/shared">Shared</a><div class="result__snippet">From Duck</div></div><div class="result"><a class="result__a" href="https://example.com/duck">Duck only</a></div>`)
			return
		}
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	searcher := &webSearcher{
		client:  server.Client(),
		engines: []searchEngine{duckDuckGoSearchEngine{endpoint: server.URL + "/duck"}, bingSearchEngine{endpoint: server.URL + "/bing"}},
	}
	results, err := searcher.Search(context.Background(), "weaveflow", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 2 || results[0].URL != "https://example.com/shared" || results[1].URL != "https://example.com/duck" {
		t.Fatalf("merged results = %#v", results)
	}
}

func TestMergeSearchResultsNormalizesAndDeduplicatesURLs(t *testing.T) {
	results := mergeSearchResults([][]searchResult{
		{{Title: " One ", URL: "HTTPS://EXAMPLE.COM/", Snippet: " first ", Engine: "duckduckgo"}},
		{{Title: "Duplicate", URL: "https://example.com/#fragment", Engine: "bing"}, {Title: "Second", URL: "https://example.org", Engine: "bing"}},
	}, 10)
	if len(results) != 2 || results[0].Title != "One" || results[0].Snippet != "first" || results[1].URL != "https://example.org" {
		t.Fatalf("normalized results = %#v", results)
	}
}

func base64RawURL(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}
