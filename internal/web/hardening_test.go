package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tinychain/mcp"
)

func TestAllToolSchemasAreObjectsAndRequiredFieldsExist(t *testing.T) {
	tools, err := New(Config{Workspace: t.TempDir(), CDPEndpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range tools.Tools() {
		seen[tool.Name] = true
		schema := tool.InputSchema
		if schema == nil {
			t.Fatalf("%s schema is nil", tool.Name)
		}
		if schema["type"] != "object" {
			t.Fatalf("%s schema type = %#v, want object", tool.Name, schema["type"])
		}
		if schema["additionalProperties"] != false {
			t.Fatalf("%s schema must set additionalProperties=false: %#v", tool.Name, schema)
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s schema missing properties map: %#v", tool.Name, schema)
		}
		required, ok := schema["required"].([]string)
		if !ok {
			t.Fatalf("%s schema required has unexpected type %T", tool.Name, schema["required"])
		}
		for _, field := range required {
			if _, exists := properties[field]; !exists {
				t.Fatalf("%s schema required field %q missing from properties %#v", tool.Name, field, properties)
			}
		}
		if _, err := json.Marshal(schema); err != nil {
			t.Fatalf("%s schema is not JSON-marshalable: %v", tool.Name, err)
		}
	}

	wantTools := []string{
		"web_workspace_info", "list_search_providers", "web_search", "fetch_url",
		"browser_status", "browser_attach", "browser_launch", "browser_navigate",
		"browser_read", "browser_screenshot", "browser_query", "browser_click",
		"browser_type", "browser_tabs", "browser_eval",
	}
	if len(seen) != len(wantTools) {
		t.Fatalf("expected %d tools, got %d: %#v", len(wantTools), len(seen), seen)
	}
	for _, name := range wantTools {
		if !seen[name] {
			t.Fatalf("missing tool %s; got %#v", name, seen)
		}
	}
}

func TestRequiredArgumentValidationIsConsistent(t *testing.T) {
	tools, err := New(Config{Workspace: t.TempDir(), CDPEndpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		tool mcp.Tool
		args map[string]any
		code string
	}{
		{name: "web_search query", tool: tools.webSearchTool(), args: map[string]any{"query": "   "}, code: "invalid_argument"},
		{name: "fetch_url url", tool: tools.fetchURLTool(), args: map[string]any{}, code: "invalid_argument"},
		{name: "browser_navigate url", tool: tools.browserNavigateTool(), args: map[string]any{}, code: "invalid_argument"},
		{name: "browser_screenshot output", tool: tools.browserScreenshotTool(), args: map[string]any{}, code: "invalid_argument"},
		{name: "browser_query selector", tool: tools.browserQueryTool(), args: map[string]any{}, code: "invalid_argument"},
		{name: "browser_type text", tool: tools.browserTypeTool(), args: map[string]any{"text": ""}, code: "invalid_argument"},
		{name: "browser_eval expression after gate", tool: newEvalTools(t, true).browserEvalTool(), args: map[string]any{}, code: "invalid_argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := callToolPayload(t, tc.tool, tc.args)
			if payload["ok"] != false || payload["code"] != tc.code {
				t.Fatalf("expected %s payload, got %#v", tc.code, payload)
			}
		})
	}
}

func TestWorkspaceOutputSafetyHardening(t *testing.T) {
	workspace := t.TempDir()
	tools, err := New(Config{Workspace: workspace, CDPEndpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(t.TempDir(), "escape.png")
	badPaths := []string{
		"../escape.png",
		"..\\escape.png",
		"screens/../../escape.png",
		"",
		".",
		outside,
		filepath.VolumeName(workspace) + string(os.PathSeparator) + "escape.png",
		"screens/not-a-png.jpg",
	}
	for _, outputPath := range badPaths {
		t.Run(outputPath, func(t *testing.T) {
			payload := callToolPayload(t, tools.browserScreenshotTool(), map[string]any{"output_path": outputPath})
			if payload["ok"] != false {
				t.Fatalf("expected failure for unsafe output_path %q, got %#v", outputPath, payload)
			}
			code := payload["code"]
			if code != "invalid_output_path" && code != "invalid_argument" {
				t.Fatalf("expected invalid path/argument code for %q, got %#v", outputPath, payload)
			}
		})
	}

	symlink := filepath.Join(workspace, "link-out")
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, symlink); err != nil {
		t.Logf("skipping symlink escape assertion; symlink creation unavailable: %v", err)
	} else {
		payload := callToolPayload(t, tools.browserScreenshotTool(), map[string]any{"output_path": "link-out/shot.png"})
		if payload["ok"] != false || payload["code"] != "invalid_output_path" {
			t.Fatalf("expected symlink parent escape rejected, got %#v", payload)
		}
	}

	payload := callToolPayload(t, tools.browserScreenshotTool(), map[string]any{"output_path": "screens/safe.png"})
	if payload["ok"] != false || payload["code"] != "browser_screenshot_failed" || !strings.Contains(payload["message"].(string), "not attached") {
		t.Fatalf("expected safe output path to pass path validation then fail not-attached, got %#v", payload)
	}
}

func TestNetworkSafetyHardening(t *testing.T) {
	badURLs := []string{
		"mailto:test@example.com",
		"file:///etc/passwd",
		"http://%zz",
		"http://",
		"https://user:pass@example.com",
		"http://localhost",
		"http://127.0.0.1",
		"http://10.0.0.1",
		"http://172.16.0.1",
		"http://192.168.1.1",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
		"http://[fe80::1]/",
	}
	for _, rawURL := range badURLs {
		if _, err := validateFetchURL(rawURL); err == nil {
			t.Fatalf("expected unsafe/malformed URL %q rejected", rawURL)
		}
	}

	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "172.16.1.1", "192.168.1.1", "169.254.169.254", "::1", "fe80::1"} {
		if !isBlockedIP(net.ParseIP(ip)) {
			t.Fatalf("expected IP %s blocked", ip)
		}
	}

	redirectToLocal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer redirectToLocal.Close()
	tools, err := New(Config{Workspace: t.TempDir(), HTTPClient: &http.Client{Transport: localServerTransport(t, redirectToLocal)}})
	if err != nil {
		t.Fatal(err)
	}
	payload := callToolPayload(t, tools.fetchURLTool(), map[string]any{"url": "http://example.com/start", "output_mode": "headers"})
	if payload["ok"] != false || payload["code"] != "fetch_failed" || !strings.Contains(payload["message"].(string), "non-public") {
		t.Fatalf("expected unsafe redirect fetch failure, got %#v", payload)
	}

	capServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("x", 128)))
	}))
	defer capServer.Close()
	tools, err = New(Config{Workspace: t.TempDir(), MaxFetchBytes: 32, HTTPClient: &http.Client{Transport: localServerTransport(t, capServer)}})
	if err != nil {
		t.Fatal(err)
	}
	payload = callToolPayload(t, tools.fetchURLTool(), map[string]any{"url": "http://example.com/big", "output_mode": "text", "output_limit": float64(16)})
	if payload["ok"] != true {
		t.Fatalf("expected capped fetch success, got %#v", payload)
	}
	fetch := payload["fetch"].(map[string]any)
	if fetch["fetch_truncated"] != true || fetch["bytes_read"] != float64(32) || fetch["text_truncated"] != true {
		t.Fatalf("expected fetch and text caps, got %#v", fetch)
	}
}

func TestProviderParserAndStubHardening(t *testing.T) {
	bravePayload := braveSearchResponse{}
	bravePayload.Web.Results = []braveWebResult{
		{Title: "\n <b>One</b> ", URL: "https://example.com/1", Description: "First <em>snippet</em>", Language: "en", Age: "today", ExtraSnippets: []string{"extra"}},
		{Title: "No URL", Description: "skip"},
		{Title: "Two", URL: "https://example.com/2", Description: "Second"},
	}
	results := normalizeBraveResults(bravePayload, 20)
	if len(results) != 2 || results[0].Title != "One" || results[0].Snippet != "First snippet" || results[0].Rank != 1 || results[1].Rank != 2 {
		t.Fatalf("unexpected normalized Brave sample results: %#v", results)
	}
	if results[0].Metadata["extra_snippets"] == nil || results[0].Metadata["language"] != "en" {
		t.Fatalf("expected Brave metadata preserved, got %#v", results[0].Metadata)
	}

	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{name: "tavily", cfg: Config{Workspace: t.TempDir(), SearchProvider: "tavily", TavilyAPIKey: "key"}},
		{name: "serpapi", cfg: Config{Workspace: t.TempDir(), SearchProvider: "serpapi", SerpAPIKey: "key"}},
		{name: "bing", cfg: Config{Workspace: t.TempDir(), SearchProvider: "bing", BingAPIKey: "key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools, err := New(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			provider := tools.providerByName(tc.name)
			if provider == nil {
				t.Fatalf("missing provider %s", tc.name)
			}
			if !provider.Configured() || provider.Supported() {
				t.Fatalf("expected configured unsupported stub for %s, got %#v", tc.name, provider.Status())
			}
			if _, err := provider.Search(context.Background(), "sample", SearchOptions{Limit: 2}); err == nil || !strings.Contains(err.Error(), "not implemented") {
				t.Fatalf("expected not implemented search error for %s, got %v", tc.name, err)
			}
		})
	}
}

func TestAllBrowserToolsNoLiveResponsesAreUseful(t *testing.T) {
	tools, err := New(Config{Workspace: t.TempDir(), CDPEndpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		tool       mcp.Tool
		args       map[string]any
		wantOK     bool
		wantCode   string
		wantPhrase string
	}{
		{name: "browser_status", tool: tools.browserStatusTool(), args: map[string]any{}, wantOK: true, wantPhrase: "remote-debugging-port"},
		{name: "browser_attach", tool: tools.browserAttachTool(), args: map[string]any{}, wantOK: false, wantCode: "browser_attach_failed", wantPhrase: "CDP"},
		{name: "browser_launch", tool: tools.browserLaunchTool(), args: map[string]any{"browser_path": "definitely-not-real-browser"}, wantOK: false, wantCode: "browser_launch_failed", wantPhrase: "browser_path"},
		{name: "browser_navigate", tool: tools.browserNavigateTool(), args: map[string]any{"url": "https://example.com"}, wantOK: false, wantCode: "browser_navigate_failed", wantPhrase: "not attached"},
		{name: "browser_read", tool: tools.browserReadTool(), args: map[string]any{}, wantOK: false, wantCode: "browser_read_failed", wantPhrase: "not attached"},
		{name: "browser_screenshot", tool: tools.browserScreenshotTool(), args: map[string]any{"output_path": "safe.png"}, wantOK: false, wantCode: "browser_screenshot_failed", wantPhrase: "not attached"},
		{name: "browser_query", tool: tools.browserQueryTool(), args: map[string]any{"selector": "body"}, wantOK: false, wantCode: "browser_query_failed", wantPhrase: "not attached"},
		{name: "browser_click", tool: tools.browserClickTool(), args: map[string]any{"selector": "button"}, wantOK: false, wantCode: "browser_click_failed", wantPhrase: "not attached"},
		{name: "browser_type", tool: tools.browserTypeTool(), args: map[string]any{"selector": "input", "text": "hello"}, wantOK: false, wantCode: "browser_type_failed", wantPhrase: "not attached"},
		{name: "browser_tabs", tool: tools.browserTabsTool(), args: map[string]any{"action": "list"}, wantOK: false, wantCode: "browser_tabs_failed", wantPhrase: "CDP"},
		{name: "browser_eval", tool: tools.browserEvalTool(), args: map[string]any{"expression": "document.title"}, wantOK: false, wantCode: "browser_eval_disabled", wantPhrase: "--allow-eval=true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := callToolPayload(t, tc.tool, tc.args)
			if payload["ok"] != tc.wantOK {
				t.Fatalf("expected ok=%v, got %#v", tc.wantOK, payload)
			}
			if !tc.wantOK && payload["code"] != tc.wantCode {
				t.Fatalf("expected code %s, got %#v", tc.wantCode, payload)
			}
			blob, _ := json.Marshal(payload)
			if !strings.Contains(string(blob), tc.wantPhrase) {
				t.Fatalf("expected payload to contain %q, got %s", tc.wantPhrase, blob)
			}
		})
	}
}

func newEvalTools(t *testing.T, allow bool) *WebTools {
	t.Helper()
	tools, err := New(Config{Workspace: t.TempDir(), AllowEval: allow, CDPEndpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	return tools
}

func callToolPayload(t *testing.T, tool mcp.Tool, args map[string]any) map[string]any {
	t.Helper()
	result, err := tool.Handler(context.Background(), args)
	if err != nil {
		t.Fatalf("%s handler returned error: %v", tool.Name, err)
	}
	if len(result.Content) == 0 {
		t.Fatalf("%s returned no content", tool.Name)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("%s returned non-JSON payload %q: %v", tool.Name, result.Content[0].Text, err)
	}
	return payload
}
