package web

import (
	"context"
	"strings"

	"tinychain/mcp"
)

func (w *WebTools) listSearchProvidersTool() mcp.Tool {
	return mcp.Tool{
		Name:        "list_search_providers",
		Description: "List supported/configured web search providers and the currently selected provider. Reports key presence only, never API key values.",
		InputSchema: schema(map[string]any{}),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			return mcp.Text(pretty(okPayload(map[string]any{
				"configured_provider": w.searchProvider,
				"selected_provider":   w.selectedProvider(),
				"providers":           w.providerStatuses(),
				"keys_present":        w.keyStatus(),
			}))), nil
		},
	}
}

func (w *WebTools) webSearchTool() mcp.Tool {
	return mcp.Tool{
		Name:        "web_search",
		Description: "Search the web with the configured provider. Brave Search is implemented; Tavily, SerpAPI, and Bing report clear unsupported/not-configured errors in this build.",
		InputSchema: schema(map[string]any{
			"query":    stringProp("Search query."),
			"limit":    integerProp("Maximum results to return. Defaults to 10, capped at 20."),
			"provider": stringEnumProp("Optional provider override: auto, brave, tavily, serpapi, bing, or none.", "auto", "brave", "tavily", "serpapi", "bing", "none"),
		}, "query"),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			query, err := requiredTextArg(args, "query")
			if err != nil {
				return mcp.Text(pretty(errorPayload("invalid_argument", err.Error(), nil))), nil
			}
			limit := intArgRange(args, "limit", defaultSearchLimit, 1, 20)
			providerName := strings.ToLower(strings.TrimSpace(textArg(args, "provider")))
			var provider SearchProvider
			if providerName == "" || providerName == "auto" {
				provider, err = w.selectedSearchProvider()
			} else if providerName == "none" {
				err = errNoProviderSelected()
			} else {
				provider = w.providerByName(providerName)
				if provider == nil {
					err = errUnknownProvider(providerName)
				} else if !provider.Configured() {
					err = errProviderNotConfigured(providerName)
				} else if !provider.Supported() {
					err = errProviderUnsupported(providerName)
				}
			}
			if err != nil {
				return mcp.Text(pretty(errorPayload("search_provider_unavailable", err.Error(), map[string]any{"providers": w.providerStatuses()}))), nil
			}
			results, err := provider.Search(ctx, query, SearchOptions{Limit: limit})
			if err != nil {
				return mcp.Text(pretty(errorPayload("search_failed", err.Error(), map[string]any{"provider": provider.Name()}))), nil
			}
			return mcp.Text(pretty(okPayload(map[string]any{
				"query":    query,
				"provider": provider.Name(),
				"count":    len(results),
				"results":  results,
			}))), nil
		},
	}
}

func (w *WebTools) fetchURLTool() mcp.Tool {
	return mcp.Tool{
		Name:        "fetch_url",
		Description: "Fetch an http/https URL safely, rejecting localhost/private targets, enforcing byte caps/timeouts/redirect limits, and returning headers plus readable text for HTML where possible.",
		InputSchema: schema(map[string]any{
			"url":          stringProp("Public http/https URL to fetch. Localhost, private, link-local, and credentialed URLs are rejected."),
			"output_mode":  stringEnumProp("Output mode. readability (default) extracts clean text from HTML; text/raw returns body text; headers returns metadata only.", "readability", "text", "raw", "headers", "none"),
			"output_limit": integerProp("Maximum output text bytes. Defaults to 65536 and is capped at 131072."),
		}, "url"),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			rawURL, err := requiredTextArg(args, "url")
			if err != nil {
				return mcp.Text(pretty(errorPayload("invalid_argument", err.Error(), nil))), nil
			}
			mode := textArg(args, "output_mode")
			limit := intArgRange(args, "output_limit", defaultOutputBytes, 1, maxReadabilityBytes)
			response, err := w.fetchURL(ctx, rawURL, mode, limit)
			if err != nil {
				return mcp.Text(pretty(errorPayload("fetch_failed", err.Error(), nil))), nil
			}
			return mcp.Text(pretty(okPayload(map[string]any{"fetch": response}))), nil
		},
	}
}

func errNoProviderSelected() error         { return simpleError("no search provider selected") }
func errUnknownProvider(name string) error { return simpleError("unknown search provider " + name) }
func errProviderNotConfigured(name string) error {
	return simpleError("search provider " + name + " is not configured; provide an API key or choose another provider")
}
func errProviderUnsupported(name string) error {
	return simpleError("search provider " + name + " is not implemented in this build")
}

type simpleError string

func (e simpleError) Error() string { return string(e) }
