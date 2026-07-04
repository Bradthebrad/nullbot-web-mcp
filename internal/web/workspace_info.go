package web

import (
	"context"

	"github.com/Bradthebrad/tinychain/mcp"
)

const defaultCDPEndpoint = "http://127.0.0.1:9222"

func (w *WebTools) webWorkspaceInfoTool() mcp.Tool {
	return mcp.Tool{
		Name:        "web_workspace_info",
		Description: "Describe web tools workspace, path policy, search provider/key status, browser attach defaults/status, eval gate, fetch cap, and current tool list.",
		InputSchema: schema(map[string]any{}),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			return mcp.Text(pretty(map[string]any{
				"workspace": map[string]any{
					"root": w.root,
					"path_policy": map[string]any{
						"relative_paths_only":      true,
						"absolute_paths_allowed":   false,
						"parent_traversal_allowed": false,
						"symlink_escape_allowed":   false,
						"writes_create_parents":    true,
						"description":              "All file paths accepted by tools must be workspace-relative and must remain inside the configured workspace after cleaning and symlink checks.",
					},
				},
				"search": map[string]any{
					"configured_provider": w.searchProvider,
					"selected_provider":   w.selectedProvider(),
					"provider_status":     w.providerStatus(),
					"providers":           w.providerStatuses(),
					"keys_present":        w.keyStatus(),
					"key_policy":          "Only boolean key-presence is reported; API key values are never returned.",
				},
				"browser":         w.browserStatusPayload(ctx),
				"allow_eval":      w.allowEval,
				"max_fetch_bytes": w.maxFetchBytes,
				"tools":           w.toolNames(),
			})), nil
		},
	}
}

func (w *WebTools) browserStatusPayload(ctx context.Context) map[string]any {
	status := w.browser.Status(ctx, "")
	return map[string]any{
		"control_path":             "cdp_attach",
		"default_cdp_endpoint":     defaultCDPEndpoint,
		"configured_cdp_endpoint":  w.cdpEndpoint,
		"browser_path":             w.browserPath,
		"localhost_binding_policy": "CDP endpoints must be bound to 127.0.0.1/localhost only.",
		"status":                   status,
	}
}
