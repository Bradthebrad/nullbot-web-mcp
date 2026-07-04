package web

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeBraveResults(t *testing.T) {
	payload := braveSearchResponse{}
	payload.Web.Results = []braveWebResult{
		{Title: "<b>Example</b> Title", URL: "https://example.com/one", Description: "A <em>snippet</em>", Language: "en", Age: "1 day ago"},
		{Title: "", URL: "https://example.com/skip", Description: "skip"},
		{Title: "Second", URL: "https://example.com/two", Description: "Second snippet"},
	}
	results := normalizeBraveResults(payload, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Example Title" || results[0].Snippet != "A snippet" || results[0].Rank != 1 || results[0].Provider != "brave" {
		t.Fatalf("unexpected first result: %#v", results[0])
	}
	if results[1].Rank != 2 {
		t.Fatalf("expected second rank 2, got %d", results[1].Rank)
	}
}

func TestBraveSearchParsesProviderJSONUsingLocalHTTPClient(t *testing.T) {
	var capturedPath, capturedQuery, capturedToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedQuery = r.URL.Query().Get("q")
		capturedToken = r.Header.Get("X-Subscription-Token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"query":{"original":"nullbot"},
			"web":{"results":[
				{"title":"<b>NullBot</b> Docs","url":"https://example.com/docs","description":"Official <em>docs</em>","age":"today","language":"en","family_friendly":true,"profile":{"name":"Example","long_name":"Example Site"},"extra_snippets":["one","two"]},
				{"title":"Skip missing URL","description":"skip me"},
				{"title":"Second Result","url":"https://example.com/two","description":"Second snippet"}
			]}
		}`)
	}))
	defer server.Close()

	provider := &braveProvider{apiKey: "local-key", client: server.Client()}
	oldEndpoint := braveSearchEndpoint
	braveSearchEndpoint = server.URL + "/res/v1/web/search"
	t.Cleanup(func() { braveSearchEndpoint = oldEndpoint })

	results, err := provider.Search(context.Background(), " nullbot ", SearchOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if capturedPath != "/res/v1/web/search" || capturedQuery != "nullbot" || capturedToken != "local-key" {
		t.Fatalf("unexpected request path/query/token: path=%q query=%q token=%q", capturedPath, capturedQuery, capturedToken)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 normalized results, got %d: %#v", len(results), results)
	}
	first := results[0]
	if first.Title != "NullBot Docs" || first.URL != "https://example.com/docs" || first.Snippet != "Official docs" || first.Rank != 1 || first.Provider != "brave" {
		t.Fatalf("unexpected first result: %#v", first)
	}
	if first.Metadata == nil || first.Metadata["age"] != "today" || first.Metadata["language"] != "en" {
		t.Fatalf("expected Brave metadata preserved, got %#v", first.Metadata)
	}
	if first.Metadata["family_friendly"] != true {
		t.Fatalf("expected Brave family_friendly bool preserved, got %#v", first.Metadata)
	}
	if results[1].Title != "Second Result" || results[1].Rank != 2 {
		t.Fatalf("expected malformed item skipped and second item reranked, got %#v", results[1])
	}
}

func TestProviderSelection(t *testing.T) {
	w, err := New(Config{Workspace: ".", SearchProvider: "auto", TavilyAPIKey: "tav-key"})
	if err != nil {
		t.Fatal(err)
	}
	if got := w.selectedProvider(); got != "tavily" {
		t.Fatalf("expected tavily, got %q", got)
	}
	w, err = New(Config{Workspace: ".", SearchProvider: "auto", BraveAPIKey: "brave-key", TavilyAPIKey: "tav-key"})
	if err != nil {
		t.Fatal(err)
	}
	if got := w.selectedProvider(); got != "brave" {
		t.Fatalf("expected brave priority, got %q", got)
	}
	w, err = New(Config{Workspace: ".", SearchProvider: "bing", BraveAPIKey: "brave-key"})
	if err != nil {
		t.Fatal(err)
	}
	if got := w.selectedProvider(); got != "bing" {
		t.Fatalf("expected explicit bing, got %q", got)
	}
}

func TestValidateFetchURL(t *testing.T) {
	good := []string{"https://example.com/path", "http://93.184.216.34/"}
	for _, value := range good {
		if _, err := validateFetchURL(value); err != nil {
			t.Fatalf("expected %q valid: %v", value, err)
		}
	}
	bad := []string{"", "ftp://example.com", "https://user:pass@example.com", "http://localhost", "http://localhost.", "http://127.0.0.1", "http://10.0.0.1", "http://172.16.0.1", "http://192.168.1.1", "http://169.254.169.254", "http://224.0.0.1", "http://0.0.0.0", "http://[::1]/", "http://[fe80::1]/", "http://[::ffff:127.0.0.1]/"}
	for _, value := range bad {
		if _, err := validateFetchURL(value); err == nil {
			t.Fatalf("expected %q invalid", value)
		}
	}
}

func TestFetchURLCapsAndReadability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>X</title><style>.x{}</style><script>bad()</script></head><body><header>Skip header</header><main><h1>Hello</h1><p>This is useful text.</p></main><footer>Skip footer</footer></body></html>`))
	}))
	defer server.Close()
	transport := localServerTransport(t, server)
	tools, err := New(Config{Workspace: ".", MaxFetchBytes: 80, HTTPClient: &http.Client{Transport: transport, Timeout: fetchTimeout}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tools.fetchURL(context.Background(), "http://example.com/test", "readability", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !result.FetchTruncated || result.BytesRead != 80 {
		t.Fatalf("expected fetch cap at 80 bytes, got truncated=%v bytes=%d", result.FetchTruncated, result.BytesRead)
	}
	if strings.Contains(result.ReadabilityText, "bad()") || strings.Contains(result.ReadabilityText, "Skip footer") {
		t.Fatalf("expected noise removed, got %q", result.ReadabilityText)
	}
}

func TestFetchURLRejectsUnsafeRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1/private", http.StatusFound)
	}))
	defer server.Close()
	tools, err := New(Config{Workspace: ".", HTTPClient: &http.Client{Transport: localServerTransport(t, server)}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tools.fetchURL(context.Background(), "http://example.com/start", "headers", 100)
	if err == nil || !strings.Contains(err.Error(), "localhost") {
		t.Fatalf("expected unsafe redirect error, got %v", err)
	}
}

func TestFetchURLRedirectLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com"+r.URL.Path+"x", http.StatusFound)
	}))
	defer server.Close()
	tools, err := New(Config{Workspace: ".", HTTPClient: &http.Client{Transport: localServerTransport(t, server)}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tools.fetchURL(context.Background(), "http://example.com/r", "headers", 100)
	if err == nil || !strings.Contains(err.Error(), "stopped after 5 redirects") {
		t.Fatalf("expected redirect limit error, got %v", err)
	}
}

func TestFetchURLHeaderSubsetAndHeadersMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Set-Cookie", "secret=value")
		w.Header().Set("X-Internal-Secret", "do-not-return")
		_, _ = w.Write([]byte("hello world"))
	}))
	defer server.Close()
	tools, err := New(Config{Workspace: ".", HTTPClient: &http.Client{Transport: localServerTransport(t, server)}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tools.fetchURL(context.Background(), "http://example.com/plain", "headers", 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.MediaType != "text/plain" || result.ContentType == "" {
		t.Fatalf("expected content type metadata, got %#v", result)
	}
	if result.Text != "" || result.ReadabilityText != "" || result.TextBytes != 0 {
		t.Fatalf("expected headers mode to omit body text, got %#v", result)
	}
	if result.Headers["Content-Type"] == nil || result.Headers["Cache-Control"] == nil || result.Headers["ETag"] == nil {
		t.Fatalf("expected allowed headers, got %#v", result.Headers)
	}
	if _, ok := result.Headers["Set-Cookie"]; ok {
		t.Fatalf("Set-Cookie should not be returned: %#v", result.Headers)
	}
	if _, ok := result.Headers["X-Internal-Secret"]; ok {
		t.Fatalf("X-Internal-Secret should not be returned: %#v", result.Headers)
	}
}

func TestFetchHTTPClientAppliesTimeoutRedirectPolicyAndDefaultTransport(t *testing.T) {
	custom := &http.Client{}
	tools, err := New(Config{Workspace: ".", HTTPClient: custom})
	if err != nil {
		t.Fatal(err)
	}
	client := tools.fetchHTTPClient()
	if client == custom {
		t.Fatal("expected fetchHTTPClient to copy injected client instead of mutating it")
	}
	if client.Timeout != fetchTimeout {
		t.Fatalf("expected default timeout %v, got %v", fetchTimeout, client.Timeout)
	}
	if client.Transport == nil {
		t.Fatal("expected default safe transport")
	}
	if client.CheckRedirect == nil {
		t.Fatal("expected redirect policy")
	}
}

func TestHTMLToReadableText(t *testing.T) {
	input := `<html><body><nav>menu</nav><article><h1>Title</h1><p>Alpha <b>beta</b>.</p><script>nope</script></article><aside>ad</aside></body></html>`
	text := htmlToReadableText(strings.NewReader(input))
	if !strings.Contains(text, "Title") || !strings.Contains(text, "Alpha") {
		t.Fatalf("expected article text, got %q", text)
	}
	if strings.Contains(text, "menu") || strings.Contains(text, "nope") || strings.Contains(text, "ad") {
		t.Fatalf("expected noise removed, got %q", text)
	}
}

func TestListSearchProvidersToolPayload(t *testing.T) {
	tools, err := New(Config{Workspace: ".", BraveAPIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	tool := tools.listSearchProvidersTool()
	result, err := tool.Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != true || payload["selected_provider"] != "brave" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAdditionalProviderStubsStatusAndErrors(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		withKey    Config
		withoutKey Config
	}{
		{name: "tavily", provider: "tavily", withKey: Config{Workspace: ".", SearchProvider: "tavily", TavilyAPIKey: "tav-key"}, withoutKey: Config{Workspace: ".", SearchProvider: "tavily"}},
		{name: "serpapi", provider: "serpapi", withKey: Config{Workspace: ".", SearchProvider: "serpapi", SerpAPIKey: "serp-key"}, withoutKey: Config{Workspace: ".", SearchProvider: "serpapi"}},
		{name: "bing", provider: "bing", withKey: Config{Workspace: ".", SearchProvider: "bing", BingAPIKey: "bing-key"}, withoutKey: Config{Workspace: ".", SearchProvider: "bing"}},
	}

	for _, tc := range tests {
		t.Run(tc.name+" not configured", func(t *testing.T) {
			tools, err := New(tc.withoutKey)
			if err != nil {
				t.Fatal(err)
			}
			provider := tools.providerByName(tc.provider)
			if provider == nil {
				t.Fatalf("provider %q not found", tc.provider)
			}
			status := provider.Status()
			if status.Configured || status.Supported {
				t.Fatalf("expected unconfigured unsupported provider, got %#v", status)
			}
			if !strings.Contains(status.Reason, "API key is not configured") {
				t.Fatalf("expected not-configured reason, got %q", status.Reason)
			}
			_, err = tools.selectedSearchProvider()
			if err == nil || !strings.Contains(err.Error(), "not configured") {
				t.Fatalf("expected not-configured selected provider error, got %v", err)
			}
		})

		t.Run(tc.name+" unsupported", func(t *testing.T) {
			tools, err := New(tc.withKey)
			if err != nil {
				t.Fatal(err)
			}
			provider := tools.providerByName(tc.provider)
			if provider == nil {
				t.Fatalf("provider %q not found", tc.provider)
			}
			status := provider.Status()
			if !status.Configured || status.Supported {
				t.Fatalf("expected configured unsupported provider, got %#v", status)
			}
			if !strings.Contains(status.Reason, "not implemented") {
				t.Fatalf("expected unsupported reason, got %q", status.Reason)
			}
			_, err = tools.selectedSearchProvider()
			if err == nil || !strings.Contains(err.Error(), "not implemented") {
				t.Fatalf("expected unsupported selected provider error, got %v", err)
			}
		})
	}
}

func TestWebSearchProviderOverrideErrors(t *testing.T) {
	tools, err := New(Config{Workspace: "."})
	if err != nil {
		t.Fatal(err)
	}
	tool := tools.webSearchTool()
	result, err := tool.Handler(context.Background(), map[string]any{"query": "example", "provider": "tavily"})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != false || payload["code"] != "search_provider_unavailable" {
		t.Fatalf("unexpected provider error payload: %#v", payload)
	}
	if !strings.Contains(payload["message"].(string), "not configured") {
		t.Fatalf("expected not-configured message, got %#v", payload["message"])
	}

	tools, err = New(Config{Workspace: ".", SearchProvider: "serpapi", SerpAPIKey: "serp-key"})
	if err != nil {
		t.Fatal(err)
	}
	tool = tools.webSearchTool()
	result, err = tool.Handler(context.Background(), map[string]any{"query": "example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != false || payload["code"] != "search_provider_unavailable" {
		t.Fatalf("unexpected unsupported provider payload: %#v", payload)
	}
	if !strings.Contains(payload["message"].(string), "not implemented") {
		t.Fatalf("expected unsupported message, got %#v", payload["message"])
	}
}

func TestHTMLToReadableTextMarkdownStructureAndCapReadyOutput(t *testing.T) {
	input := `<html>
		<head><title>Browser title should not leak</title><meta name="description" content="hidden"></head>
		<body>
			<div class="cookie banner">Accept cookies</div>
			<header>Global header</header>
			<main>
				<article>
					<h1>Readable Title</h1>
					<p>First <strong>paragraph</strong> with <a href="/x">inline link</a>.</p>
					<ul><li>First item</li><li>Second item</li></ul>
					<p aria-hidden="true">Hidden copy</p>
					<div role="navigation">Role nav</div>
				</article>
			</main>
			<aside class="related">Related links</aside>
			<footer>Global footer</footer>
		</body>
	</html>`
	text := htmlToReadableText(strings.NewReader(input))
	for _, want := range []string{"# Readable Title", "First paragraph with inline link.", "- First item", "- Second item"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in readable text, got %q", want, text)
		}
	}
	for _, noise := range []string{"Browser title should not leak", "Accept cookies", "Global header", "Hidden copy", "Role nav", "Related links", "Global footer"} {
		if strings.Contains(text, noise) {
			t.Fatalf("expected noise %q removed, got %q", noise, text)
		}
	}

	capped := cappedTextResult(text, 30)
	if capped["truncated"] != true || !strings.Contains(capped["text"].(string), "[truncated]") {
		t.Fatalf("expected capped readability output to report truncation, got %#v", capped)
	}
}

func localServerTransport(t *testing.T, server *httptest.Server) *http.Transport {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	return transport
}
