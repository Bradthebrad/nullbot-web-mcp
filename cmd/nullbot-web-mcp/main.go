package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Bradthebrad/nullbot-web-mcp/internal/web"
	"tinychain/mcp"
)

const version = "0.1.0"

func main() {
	transport := flag.String("transport", "stdio", "Transport: stdio, streamable-http, http, or sse.")
	addr := flag.String("addr", "127.0.0.1:8780", "HTTP/SSE listen address.")
	path := flag.String("path", "/mcp", "Streamable HTTP endpoint path.")
	ssePath := flag.String("sse-path", "/sse", "Legacy SSE endpoint path.")
	messagePath := flag.String("message-path", "/message", "Legacy SSE message endpoint path.")
	workspace := flag.String("workspace", ".", "Workspace root. Output paths cannot escape this directory.")
	searchProvider := flag.String("search-provider", "auto", "Search provider preference: auto, brave, tavily, serpapi, bing, or none.")
	cdpEndpoint := flag.String("cdp-endpoint", "http://127.0.0.1:9222", "Chromium DevTools Protocol endpoint for browser attach.")
	browserPath := flag.String("browser-path", "", "Optional Chrome/Edge/Brave executable path for future browser_launch support.")
	allowEval := flag.Bool("allow-eval", false, "Enable future browser_eval JavaScript evaluation tool. Disabled by default.")
	braveAPIKey := flag.String("brave-api-key", "", "Brave Search API key. Defaults to BRAVE_API_KEY when empty.")
	tavilyAPIKey := flag.String("tavily-api-key", "", "Tavily API key. Defaults to TAVILY_API_KEY when empty.")
	serpAPIKey := flag.String("serpapi-api-key", "", "SerpAPI key. Defaults to SERPAPI_API_KEY when empty.")
	bingAPIKey := flag.String("bing-api-key", "", "Bing Search API key. Defaults to BING_API_KEY when empty.")
	maxFetchBytes := flag.Int64("max-fetch-bytes", 512*1024, "Maximum bytes future fetch_url/browser_read operations may return by default.")
	showVersion := flag.Bool("version", false, "Print version and exit.")
	flag.Parse()

	if *showVersion {
		fmt.Println("nullbot-web-mcp", version)
		return
	}

	webTools, err := web.New(web.Config{
		Workspace:      *workspace,
		SearchProvider: *searchProvider,
		CDPEndpoint:    *cdpEndpoint,
		BrowserPath:    *browserPath,
		AllowEval:      *allowEval,
		BraveAPIKey:    firstNonEmpty(*braveAPIKey, os.Getenv("BRAVE_API_KEY")),
		TavilyAPIKey:   firstNonEmpty(*tavilyAPIKey, os.Getenv("TAVILY_API_KEY")),
		SerpAPIKey:     firstNonEmpty(*serpAPIKey, os.Getenv("SERPAPI_API_KEY")),
		BingAPIKey:     firstNonEmpty(*bingAPIKey, os.Getenv("BING_API_KEY")),
		MaxFetchBytes:  *maxFetchBytes,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "nullbot-web-mcp:", err)
		os.Exit(2)
	}

	server := mcp.NewServer("nullbot-web-mcp")
	server.Version = version
	for _, tool := range webTools.Tools() {
		server.AddTool(tool)
	}

	if *transport != "stdio" {
		fmt.Fprintf(os.Stderr, "nullbot-web-mcp serving %s on %s\n", *transport, *addr)
	}
	err = server.Run(
		context.Background(),
		mcp.WithTransport(*transport),
		mcp.WithAddr(*addr),
		mcp.WithPath(*path),
		mcp.WithSSEPaths(*ssePath, *messagePath),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nullbot-web-mcp:", err)
		os.Exit(1)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
