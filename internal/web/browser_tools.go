package web

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tinychain/mcp"
)

func (w *WebTools) browserStatusTool() mcp.Tool {
	return mcp.Tool{
		Name:        "browser_status",
		Description: "Report Chromium CDP endpoint reachability, attach state, active page, known tabs/targets, and actionable launch/attach instructions without requiring a live browser.",
		InputSchema: schema(map[string]any{
			"endpoint": stringProp("Optional localhost CDP endpoint override such as http://127.0.0.1:9222."),
		}),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			status := w.browser.Status(ctx, textArg(args, "endpoint"))
			return mcp.Text(pretty(okPayload(map[string]any{"browser": status}))), nil
		},
	}
}

func (w *WebTools) browserAttachTool() mcp.Tool {
	return mcp.Tool{
		Name:        "browser_attach",
		Description: "Attach to an already-running localhost Chromium CDP endpoint. Start Chrome/Edge/Brave with --remote-debugging-address=127.0.0.1 before calling this tool.",
		InputSchema: schema(map[string]any{
			"endpoint": stringProp("Optional localhost CDP endpoint override such as http://127.0.0.1:9222 or ws://127.0.0.1:9222/devtools/browser/<id>."),
		}),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			status, err := w.browser.Attach(ctx, textArg(args, "endpoint"))
			if err != nil {
				return mcp.Text(pretty(errorPayload("browser_attach_failed", err.Error(), map[string]any{"browser": status}))), nil
			}
			return mcp.Text(pretty(okPayload(map[string]any{"browser": status}))), nil
		},
	}
}

func (w *WebTools) browserLaunchTool() mcp.Tool {
	return mcp.Tool{
		Name:        "browser_launch",
		Description: "Opt-in browser_control launcher for Chrome/Edge/Brave. Starts with --remote-debugging-address=127.0.0.1 and an isolated temporary profile by default. Confirm before consequential browser actions.",
		InputSchema: schema(map[string]any{
			"browser_path":      stringProp("Optional Chrome/Edge/Brave executable path. Defaults to --browser-path or common installed browser paths."),
			"port":              integerProp("Localhost remote debugging port. Defaults to the configured CDP endpoint port or 9222."),
			"profile_dir":       stringProp("Optional user-data-dir/profile directory. If omitted, a temporary isolated profile is created. Non-temporary profiles may retain cookies, sessions, history, downloads, and account state."),
			"temporary_profile": boolProp("Create an isolated temporary profile, ignoring profile_dir. Defaults to false, but an omitted profile_dir still creates a temporary profile."),
		}),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			browserPath := textArg(args, "browser_path")
			if strings.TrimSpace(browserPath) == "" {
				browserPath = w.browserPath
			}
			launchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			result, err := w.browser.Launch(launchCtx, browserPath, intArg(args, "port", 0), textArg(args, "profile_dir"), boolArg(args, "temporary_profile"))
			if err != nil {
				return mcp.Text(pretty(errorPayload("browser_launch_failed", err.Error(), map[string]any{"hint": browserInstructionHint(w.cdpEndpoint)}))), nil
			}
			return mcp.Text(pretty(okPayload(map[string]any{"browser_launch": result}))), nil
		},
	}
}

func (w *WebTools) browserNavigateTool() mcp.Tool {
	return mcp.Tool{
		Name:        "browser_navigate",
		Description: "Navigate the active attached Chromium tab to an http/https URL and wait for the page body to become ready.",
		InputSchema: schema(map[string]any{
			"url":     stringProp("Destination http/https URL."),
			"timeout": integerProp("Timeout in seconds. Defaults to 15 and is capped at 60."),
		}, "url"),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			rawURL, err := requiredTextArg(args, "url")
			if err != nil {
				return mcp.Text(pretty(errorPayload("invalid_argument", err.Error(), nil))), nil
			}
			result, err := w.browser.Navigate(ctx, rawURL, secondsArg(args, "timeout", 15, 1, 60))
			if err != nil {
				return mcp.Text(pretty(errorPayload("browser_navigate_failed", err.Error(), map[string]any{"hint": browserInstructionHint(w.cdpEndpoint)}))), nil
			}
			return mcp.Text(pretty(okPayload(map[string]any{"navigation": result}))), nil
		},
	}
}

func (w *WebTools) browserReadTool() mcp.Tool {
	return mcp.Tool{
		Name:        "browser_read",
		Description: "Read capped text, readability text, DOM outline, or HTML from the active attached browser tab or a CSS selector.",
		InputSchema: schema(map[string]any{
			"selector":     stringProp("Optional CSS selector to read. Defaults to body."),
			"mode":         stringEnumProp("Read mode: text, readability, outline, or html.", "text", "readability", "outline", "html"),
			"output_limit": integerProp("Maximum output text bytes. Defaults to 32768 and is capped at 131072."),
			"timeout":      integerProp("Timeout in seconds. Defaults to 15 and is capped at 60."),
		}),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			result, err := w.browser.Read(ctx, textArg(args, "selector"), textArg(args, "mode"), intArgRange(args, "output_limit", defaultTextLimit, 1, maxReadabilityBytes), secondsArg(args, "timeout", 15, 1, 60))
			if err != nil {
				return mcp.Text(pretty(errorPayload("browser_read_failed", err.Error(), map[string]any{"hint": browserInstructionHint(w.cdpEndpoint)}))), nil
			}
			return mcp.Text(pretty(okPayload(map[string]any{"page": result}))), nil
		},
	}
}

func (w *WebTools) browserScreenshotTool() mcp.Tool {
	return mcp.Tool{
		Name:        "browser_screenshot",
		Description: "Capture a viewport or full-page PNG from the active attached Chromium tab to a workspace-relative output_path. Uses workspace_write safety checks.",
		InputSchema: schema(map[string]any{
			"output_path": stringProp("Workspace-relative PNG output path. Absolute paths and parent traversal are rejected."),
			"full_page":   boolProp("Capture the full page instead of the current viewport."),
			"timeout":     integerProp("Timeout in seconds. Defaults to 15 and is capped at 60."),
		}, "output_path"),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			outputPath, err := requiredTextArg(args, "output_path")
			if err != nil {
				return mcp.Text(pretty(errorPayload("invalid_argument", err.Error(), nil))), nil
			}
			fullPath, err := w.resolveOutput(outputPath)
			if err != nil {
				return mcp.Text(pretty(errorPayload("invalid_output_path", err.Error(), nil))), nil
			}
			if strings.ToLower(filepath.Ext(fullPath)) != ".png" {
				return mcp.Text(pretty(errorPayload("invalid_output_path", "output_path: browser screenshots must be saved as .png", nil))), nil
			}

			fullPage := boolArg(args, "full_page")
			timeout := secondsArg(args, "timeout", 15, 1, 60)
			started := time.Now()
			pngBytes, err := w.browser.Screenshot(ctx, fullPage, timeout)
			if err != nil {
				return mcp.Text(pretty(errorPayload("browser_screenshot_failed", err.Error(), map[string]any{"hint": browserInstructionHint(w.cdpEndpoint)}))), nil
			}
			if _, err := png.DecodeConfig(bytes.NewReader(pngBytes)); err != nil {
				return mcp.Text(pretty(errorPayload("browser_screenshot_failed", fmt.Sprintf("captured screenshot is not a valid PNG: %v", err), nil))), nil
			}
			if err := os.WriteFile(fullPath, pngBytes, 0o644); err != nil {
				return mcp.Text(pretty(errorPayload("write_failed", fmt.Sprintf("write screenshot: %v", err), nil))), nil
			}
			info, _ := os.Stat(fullPath)
			metadata := map[string]any{
				"output_path":        w.rel(fullPath),
				"absolute_path":      fullPath,
				"bytes":              len(pngBytes),
				"full_page":          fullPage,
				"capture_mode":       map[bool]string{true: "full_page", false: "viewport"}[fullPage],
				"mime_type":          "image/png",
				"timeout_ms":         timeout.Milliseconds(),
				"elapsed_ms":         time.Since(started).Milliseconds(),
				"workspace_relative": true,
			}
			if config, err := png.DecodeConfig(bytes.NewReader(pngBytes)); err == nil {
				metadata["width"] = config.Width
				metadata["height"] = config.Height
			}
			if info != nil {
				metadata["file_size"] = info.Size()
				metadata["modified_at"] = info.ModTime().UTC().Format(time.RFC3339)
			}
			return mcp.Text(pretty(okPayload(map[string]any{"screenshot": metadata}))), nil
		},
	}
}

func secondsArg(args map[string]any, key string, fallback, min, max int) time.Duration {
	return time.Duration(intArgRange(args, key, fallback, min, max)) * time.Second
}
