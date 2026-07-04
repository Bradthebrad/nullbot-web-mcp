package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeCDPEndpoint(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantHTTP  string
		wantWS    string
		wantError string
	}{
		{name: "default host port", input: "127.0.0.1:9222", wantHTTP: "http://127.0.0.1:9222"},
		{name: "schemeless localhost", input: "//localhost:9222", wantHTTP: "http://localhost:9222"},
		{name: "http localhost", input: "http://localhost:9222/", wantHTTP: "http://localhost:9222"},
		{name: "ws browser", input: "ws://127.0.0.1:9222/devtools/browser/abc", wantHTTP: "http://127.0.0.1:9222", wantWS: "ws://127.0.0.1:9222/devtools/browser/abc"},
		{name: "reject remote", input: "http://example.com:9222", wantError: "only localhost"},
		{name: "reject scheme", input: "ftp://127.0.0.1:9222", wantError: "unsupported scheme"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeCDPEndpoint(tc.input)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected error containing %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.HTTPBase != tc.wantHTTP || got.BrowserWebSocketURL != tc.wantWS || !got.Loopback {
				t.Fatalf("unexpected endpoint: %#v", got)
			}
		})
	}
}

func TestBrowserStatusNoEndpointFriendlyError(t *testing.T) {
	tools, err := New(Config{Workspace: ".", CDPEndpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	status := tools.browser.Status(context.Background(), "")
	if status.Reachable {
		t.Fatalf("expected unreachable status: %#v", status)
	}
	if status.Attached {
		t.Fatalf("expected not attached: %#v", status)
	}
	if status.InstructionHint == "" || !strings.Contains(status.InstructionHint, "remote-debugging-port") {
		t.Fatalf("expected actionable hint, got %#v", status.InstructionHint)
	}
}

func TestBrowserStatusAndAttachToolNoLivePayloads(t *testing.T) {
	tools, err := New(Config{Workspace: t.TempDir(), CDPEndpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}

	statusResult, err := tools.browserStatusTool().Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var statusPayload map[string]any
	if err := json.Unmarshal([]byte(statusResult.Content[0].Text), &statusPayload); err != nil {
		t.Fatal(err)
	}
	if statusPayload["ok"] != true {
		t.Fatalf("expected browser_status ok payload, got %#v", statusPayload)
	}
	browserStatus, _ := statusPayload["browser"].(map[string]any)
	if browserStatus["reachable"] != false || browserStatus["attached"] != false {
		t.Fatalf("expected unreachable and not attached browser status, got %#v", browserStatus)
	}
	if !strings.Contains(browserStatus["instruction_hint"].(string), "remote-debugging-port") {
		t.Fatalf("expected actionable status hint, got %#v", browserStatus["instruction_hint"])
	}

	attachResult, err := tools.browserAttachTool().Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var attachPayload map[string]any
	if err := json.Unmarshal([]byte(attachResult.Content[0].Text), &attachPayload); err != nil {
		t.Fatal(err)
	}
	if attachPayload["ok"] != false || attachPayload["code"] != "browser_attach_failed" {
		t.Fatalf("expected browser_attach_failed payload, got %#v", attachPayload)
	}
	if !strings.Contains(attachPayload["message"].(string), "probe CDP /json/version") && !strings.Contains(attachPayload["message"].(string), "CDP endpoint is not reachable") {
		t.Fatalf("expected unreachable endpoint attach message, got %#v", attachPayload["message"])
	}
	attachDetails, _ := attachPayload["details"].(map[string]any)
	attachBrowser, _ := attachDetails["browser"].(map[string]any)
	if attachBrowser["reachable"] != false || attachBrowser["attached"] != false {
		t.Fatalf("expected failed attach to report unreachable/not attached, got %#v", attachBrowser)
	}
}

func TestBrowserAttachProbeWithLocalEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/json/version":
			_, _ = w.Write([]byte(`{"Browser":"Chrome/Test","Protocol-Version":"1.3","User-Agent":"test","webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/test"}`))
		case "/json/list":
			_, _ = w.Write([]byte(`[{"id":"page-1","type":"page","title":"Example","url":"https://example.com","webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/page/page-1"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manager := NewBrowserManager(server.URL, server.Client())
	status := manager.Status(context.Background(), "")
	if !status.Reachable || status.Endpoint.BrowserWebSocketURL == "" {
		t.Fatalf("expected reachable endpoint with browser websocket, got %#v", status)
	}
	if len(status.Targets) != 1 || status.Targets[0].ID != "page-1" {
		t.Fatalf("expected target list, got %#v", status.Targets)
	}
	if status.ActiveTargetID != "page-1" || status.ActiveTarget == nil || status.CurrentURL != "https://example.com" || status.CurrentTitle != "Example" {
		t.Fatalf("expected active target URL/title, got %#v", status)
	}
	if status.Version["browser"] != "Chrome/Test" {
		t.Fatalf("expected version metadata, got %#v", status.Version)
	}
	if len(status.ActionableInstructions) == 0 || !strings.Contains(strings.Join(status.ActionableInstructions, " "), "browser_attach") {
		t.Fatalf("expected attach instructions, got %#v", status.ActionableInstructions)
	}
}

func TestBrowserStatusActionableNoWebsocket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/json/version":
			_, _ = w.Write([]byte(`{"Browser":"Chrome/Test","Protocol-Version":"1.3"}`))
		case "/json/list":
			_, _ = w.Write([]byte(`[{"id":"page-1","type":"page","title":"Example","url":"https://example.com"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manager := NewBrowserManager(server.URL, server.Client())
	status, err := manager.Attach(context.Background(), "")
	if err == nil {
		t.Fatal("expected attach error without browser websocket URL")
	}
	if !status.Reachable || status.Attached {
		t.Fatalf("expected reachable but not attached, got %#v", status)
	}
	if status.ActiveTarget == nil || status.CurrentURL != "https://example.com" || status.CurrentTitle != "Example" {
		t.Fatalf("expected active target fallback URL/title, got %#v", status)
	}
	joined := strings.Join(status.ActionableInstructions, " ")
	if !strings.Contains(joined, "webSocketDebuggerUrl") || !strings.Contains(joined, "remote-debugging-port") {
		t.Fatalf("expected websocket/remotedebugging instructions, got %#v", status.ActionableInstructions)
	}
}

func TestBrowserToolsNoLivePayloadsAndSchemas(t *testing.T) {
	tools, err := New(Config{Workspace: ".", CDPEndpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []struct {
		name string
		tool func() any
	}{
		{name: "browser_status", tool: func() any { return tools.browserStatusTool().InputSchema }},
		{name: "browser_attach", tool: func() any { return tools.browserAttachTool().InputSchema }},
		{name: "browser_launch", tool: func() any { return tools.browserLaunchTool().InputSchema }},
		{name: "browser_navigate", tool: func() any { return tools.browserNavigateTool().InputSchema }},
		{name: "browser_read", tool: func() any { return tools.browserReadTool().InputSchema }},
		{name: "browser_screenshot", tool: func() any { return tools.browserScreenshotTool().InputSchema }},
	} {
		schema, ok := tool.tool().(map[string]any)
		if !ok || schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Fatalf("%s invalid schema: %#v", tool.name, schema)
		}
		if tool.name == "browser_read" {
			properties, _ := schema["properties"].(map[string]any)
			modeProp, _ := properties["mode"].(map[string]any)
			enum, _ := modeProp["enum"].([]string)
			if len(enum) != 4 || strings.Join(enum, ",") != "text,readability,outline,html" {
				t.Fatalf("browser_read mode enum missing expected modes: %#v", modeProp["enum"])
			}
		}
	}

	result, err := tools.browserReadTool().Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != false || payload["code"] != "browser_read_failed" || !strings.Contains(payload["message"].(string), "not attached") {
		t.Fatalf("expected not-attached browser_read payload, got %#v", payload)
	}
}

func TestBrowserReadValidationAndOutlineFormatting(t *testing.T) {
	for _, mode := range []string{"text", "readability", "outline", "html"} {
		if !validBrowserReadMode(mode) {
			t.Fatalf("expected valid browser_read mode %q", mode)
		}
	}
	if validBrowserReadMode("raw") {
		t.Fatal("raw should not be a browser_read mode")
	}

	outline := htmlToDOMOutline(strings.NewReader(`<html><head><script>bad()</script></head><body><main id="content"><h1>Title</h1><a href="https://example.com/very/long/path">Read more</a><button aria-label="Save item">Save</button></main></body></html>`))
	for _, want := range []string{"<body>", "<main> #content", "<h1> — Title", "href=\"https://example.com/very/long/path\"", "aria-label=\"Save item\""} {
		if !strings.Contains(outline, want) {
			t.Fatalf("expected outline to contain %q, got:\n%s", want, outline)
		}
	}
	if strings.Contains(outline, "script") || strings.Contains(outline, "bad()") {
		t.Fatalf("expected outline to skip script/head noise, got:\n%s", outline)
	}

	tools, err := New(Config{Workspace: t.TempDir(), CDPEndpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tools.browserReadTool().Handler(context.Background(), map[string]any{"mode": "raw"})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != false || payload["code"] != "browser_read_failed" || !strings.Contains(payload["message"].(string), "expected text, readability, outline, or html") {
		t.Fatalf("expected invalid-mode browser_read payload, got %#v", payload)
	}
}

func TestBrowserScreenshotOutputPathSafetyBeforeAttach(t *testing.T) {
	tmp := t.TempDir()
	tools, err := New(Config{Workspace: tmp})
	if err != nil {
		t.Fatal(err)
	}
	bad := []string{"../escape.png", tmp + string(os.PathSeparator) + "escape.png", "notpng.txt"}
	for _, outputPath := range bad {
		result, err := tools.browserScreenshotTool().Handler(context.Background(), map[string]any{"output_path": outputPath})
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["ok"] != false || payload["code"] != "invalid_output_path" {
			t.Fatalf("expected invalid_output_path for %q, got %#v", outputPath, payload)
		}
	}

	result, err := tools.browserScreenshotTool().Handler(context.Background(), map[string]any{"output_path": "screens/shot.png"})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != false || payload["code"] != "browser_screenshot_failed" || !strings.Contains(payload["message"].(string), "not attached") {
		t.Fatalf("expected safe path then not-attached screenshot payload, got %#v", payload)
	}
}

func TestValidateBrowserURL(t *testing.T) {
	for _, value := range []string{"https://example.com", "http://example.com/path"} {
		if _, err := validateBrowserURL(value); err != nil {
			t.Fatalf("expected valid browser URL %q: %v", value, err)
		}
	}
	got, err := validateBrowserURL(" HTTPS://Example.COM/path#secret ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://Example.COM/path" {
		t.Fatalf("expected normalized URL without fragment, got %q", got)
	}
	for _, value := range []string{"", "ftp://example.com", "https://user:pass@example.com", "notaurl", "https:///missing-host"} {
		if _, err := validateBrowserURL(value); err == nil {
			t.Fatalf("expected invalid browser URL %q", value)
		}
	}
}

func TestBrowserNavigateToolValidationAndNotAttached(t *testing.T) {
	tools, err := New(Config{Workspace: t.TempDir(), CDPEndpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tools.browserNavigateTool().Handler(context.Background(), map[string]any{"url": "ftp://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != false || payload["code"] != "browser_navigate_failed" || !strings.Contains(payload["message"].(string), "http and https") {
		t.Fatalf("expected invalid-url navigate payload, got %#v", payload)
	}

	result, err = tools.browserNavigateTool().Handler(context.Background(), map[string]any{"url": "https://example.com", "timeout": float64(1)})
	if err != nil {
		t.Fatal(err)
	}
	payload = map[string]any{}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != false || payload["code"] != "browser_navigate_failed" || !strings.Contains(payload["message"].(string), "not attached") {
		t.Fatalf("expected not-attached navigate payload, got %#v", payload)
	}
}

func TestBrowserLaunchHelpersProfilePortArgsAndRisk(t *testing.T) {
	port, err := launchPort(0, "http://127.0.0.1:9333")
	if err != nil || port != 9333 {
		t.Fatalf("expected endpoint port 9333, got %d err %v", port, err)
	}
	port, err = launchPort(0, "http://127.0.0.1")
	if err != nil || port != 9222 {
		t.Fatalf("expected default port 9222, got %d err %v", port, err)
	}
	if _, err := launchPort(70000, ""); err == nil {
		t.Fatal("expected invalid port error")
	}

	tempProfile, err := prepareBrowserProfile("", false)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempProfile.Dir)
	if !tempProfile.CreatedTemp || tempProfile.Strategy != "temporary_isolated" {
		t.Fatalf("expected temporary isolated profile, got %#v", tempProfile)
	}
	if info, err := os.Stat(tempProfile.Dir); err != nil || !info.IsDir() {
		t.Fatalf("expected temp profile dir to exist: info=%#v err=%v", info, err)
	}

	providedDir := t.TempDir()
	providedProfile, err := prepareBrowserProfile(providedDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if providedProfile.CreatedTemp || providedProfile.Strategy != "provided_user_data_dir" || !filepath.IsAbs(providedProfile.Dir) {
		t.Fatalf("expected provided absolute profile strategy, got %#v", providedProfile)
	}

	args := browserLaunchArgs(9555, providedProfile.Dir)
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=9555",
		"--user-data-dir=" + providedProfile.Dir,
		"--no-first-run",
		"--no-default-browser-check",
		"about:blank",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected launch args to contain %q, got %#v", want, args)
		}
	}
	if !strings.Contains(browserLaunchRiskNote(tempProfile), "isolated temporary profile") || !strings.Contains(browserLaunchRiskNote(tempProfile), "127.0.0.1") {
		t.Fatalf("expected temporary risk note to mention isolated profile and localhost: %s", browserLaunchRiskNote(tempProfile))
	}
	if !strings.Contains(browserLaunchRiskNote(providedProfile), "caller-provided") || !strings.Contains(browserLaunchRiskNote(providedProfile), "cookies") {
		t.Fatalf("expected provided-profile risk note to mention caller-provided cookies: %s", browserLaunchRiskNote(providedProfile))
	}
}

func TestBrowserLaunchToolInvalidPathPayload(t *testing.T) {
	tools, err := New(Config{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tools.browserLaunchTool().Handler(context.Background(), map[string]any{
		"browser_path": "definitely-not-a-real-nullbot-browser",
		"port":         float64(9224),
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != false || payload["code"] != "browser_launch_failed" || !strings.Contains(payload["message"].(string), "browser_path") {
		t.Fatalf("expected browser_launch_failed invalid browser_path payload, got %#v", payload)
	}
	details, _ := payload["details"].(map[string]any)
	if !strings.Contains(details["hint"].(string), "remote-debugging-port") {
		t.Fatalf("expected launch failure hint to mention remote-debugging-port, got %#v", details)
	}
}

func TestAdvancedBrowserToolsSchemasNoLiveAndEvalGate(t *testing.T) {
	tools, err := New(Config{Workspace: t.TempDir(), CDPEndpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	advanced := []struct {
		name string
		tool func() any
	}{
		{name: "browser_query", tool: func() any { return tools.browserQueryTool().InputSchema }},
		{name: "browser_click", tool: func() any { return tools.browserClickTool().InputSchema }},
		{name: "browser_type", tool: func() any { return tools.browserTypeTool().InputSchema }},
		{name: "browser_tabs", tool: func() any { return tools.browserTabsTool().InputSchema }},
		{name: "browser_eval", tool: func() any { return tools.browserEvalTool().InputSchema }},
	}
	for _, tool := range advanced {
		schema, ok := tool.tool().(map[string]any)
		if !ok || schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Fatalf("%s invalid schema: %#v", tool.name, schema)
		}
	}
	if err := interactionSafetyDescriptionsOK(tools.Tools()); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		call func() (string, error)
		code string
		want string
	}{
		{name: "query not attached", call: func() (string, error) {
			r, err := tools.browserQueryTool().Handler(context.Background(), map[string]any{"selector": "button"})
			return r.Content[0].Text, err
		}, code: "browser_query_failed", want: "not attached"},
		{name: "click needs target", call: func() (string, error) {
			r, err := tools.browserClickTool().Handler(context.Background(), map[string]any{})
			return r.Content[0].Text, err
		}, code: "browser_click_failed", want: "selector or non-zero"},
		{name: "type not attached", call: func() (string, error) {
			r, err := tools.browserTypeTool().Handler(context.Background(), map[string]any{"text": "hello"})
			return r.Content[0].Text, err
		}, code: "browser_type_failed", want: "not attached"},
		{name: "tabs unreachable", call: func() (string, error) {
			r, err := tools.browserTabsTool().Handler(context.Background(), map[string]any{"action": "list"})
			return r.Content[0].Text, err
		}, code: "browser_tabs_failed", want: "probe CDP"},
		{name: "eval disabled", call: func() (string, error) {
			r, err := tools.browserEvalTool().Handler(context.Background(), map[string]any{"expression": "document.title"})
			return r.Content[0].Text, err
		}, code: "browser_eval_disabled", want: "--allow-eval=true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, err := tc.call()
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(text), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["ok"] != false || payload["code"] != tc.code || !strings.Contains(payload["message"].(string), tc.want) {
				t.Fatalf("expected %s containing %q, got %#v", tc.code, tc.want, payload)
			}
		})
	}
}

func TestBrowserTabsListSelectCloseProtectionWithMockCDP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/json/version":
			_, _ = w.Write([]byte(`{"Browser":"Chrome/Test","Protocol-Version":"1.3","webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/test"}`))
		case r.URL.Path == "/json/list":
			_, _ = w.Write([]byte(`[{"id":"page-1","type":"page","title":"One","url":"https://example.com/one"}]`))
		case r.URL.Path == "/json/close/page-1":
			_, _ = w.Write([]byte(`Target is closing`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manager := NewBrowserManager(server.URL, server.Client())
	listed, err := manager.Tabs(context.Background(), "list", "", "", false, browserStatusTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if listed["target_count"] != 1 || listed["active_target_id"] != "page-1" {
		t.Fatalf("expected one listed active tab, got %#v", listed)
	}
	selected, err := manager.Tabs(context.Background(), "select", "page-1", "", false, browserStatusTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if selected["selected"] != true || selected["active_target_id"] != "page-1" {
		t.Fatalf("expected selected page-1, got %#v", selected)
	}
	if _, err := manager.Tabs(context.Background(), "close", "page-1", "", false, browserStatusTimeout); err == nil || !strings.Contains(err.Error(), "last known page") {
		t.Fatalf("expected close last tab protection, got %v", err)
	}
	closed, err := manager.Tabs(context.Background(), "close", "page-1", "", true, browserStatusTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if closed["closed"] != true || closed["closed_target_id"] != "page-1" {
		t.Fatalf("expected forced close result, got %#v", closed)
	}
}
