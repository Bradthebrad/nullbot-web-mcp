package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Bradthebrad/tinychain/mcp"
)

func (w *WebTools) browserQueryTool() mcp.Tool {
	return mcp.Tool{
		Name:        "browser_query",
		Description: "Query the active attached Chromium tab by CSS selector and return capped matches with text, selected attributes, visibility hints, and stable result indexes.",
		InputSchema: schema(map[string]any{
			"selector": stringProp("Required CSS selector to query."),
			"limit":    integerProp("Maximum matches to return. Defaults to 20 and is capped at 100."),
			"timeout":  integerProp("Timeout in seconds. Defaults to 15 and is capped at 60."),
		}, "selector"),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			selector, err := requiredTextArg(args, "selector")
			if err != nil {
				return mcp.Text(pretty(errorPayload("invalid_argument", err.Error(), nil))), nil
			}
			result, err := w.browser.Query(ctx, selector, intArgRange(args, "limit", 20, 1, 100), secondsArg(args, "timeout", 15, 1, 60))
			if err != nil {
				return mcp.Text(pretty(errorPayload("browser_query_failed", err.Error(), map[string]any{"hint": browserInstructionHint(w.cdpEndpoint)}))), nil
			}
			return mcp.Text(pretty(okPayload(map[string]any{"query": result}))), nil
		},
	}
}

func (w *WebTools) browserClickTool() mcp.Tool {
	return mcp.Tool{
		Name:        "browser_click",
		Description: "browser_control: Click exactly one element by CSS selector, or click page coordinates. Confirm before purchases, sends, deletes, account changes, or other consequential actions.",
		InputSchema: schema(map[string]any{
			"selector": stringProp("CSS selector to click. Must match exactly one element. If omitted, non-zero x/y coordinates are used."),
			"x":        numberProp("Viewport x coordinate for coordinate click. Used only when selector is omitted."),
			"y":        numberProp("Viewport y coordinate for coordinate click. Used only when selector is omitted."),
			"timeout":  integerProp("Timeout in seconds. Defaults to 15 and is capped at 60."),
		}),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			result, err := w.browser.Click(ctx, textArg(args, "selector"), numberArg(args, "x", 0), numberArg(args, "y", 0), secondsArg(args, "timeout", 15, 1, 60))
			if err != nil {
				return mcp.Text(pretty(errorPayload("browser_click_failed", err.Error(), map[string]any{"hint": browserInstructionHint(w.cdpEndpoint), "risk_note": browserControlRiskNote}))), nil
			}
			return mcp.Text(pretty(okPayload(map[string]any{"click": result}))), nil
		},
	}
}

func (w *WebTools) browserTypeTool() mcp.Tool {
	return mcp.Tool{
		Name:        "browser_type",
		Description: "browser_control: Type text into a selected/focused input or a CSS selector, optionally clearing first and optionally submitting with Enter. Confirm before purchases, sends, deletes, account changes, private-data submission, consequential forms, or other consequential actions.",
		InputSchema: schema(map[string]any{
			"selector": stringProp("Optional CSS selector for the target input/element. If omitted, types into the currently focused element."),
			"text":     stringProp("Text to type. Required."),
			"clear":    boolProp("Clear the selected element before typing. Only applies when selector is provided."),
			"submit":   boolProp("Append Enter after typing, which may submit a form."),
			"timeout":  integerProp("Timeout in seconds. Defaults to 15 and is capped at 60."),
		}, "text"),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			text, err := requiredTextArg(args, "text")
			if err != nil {
				return mcp.Text(pretty(errorPayload("invalid_argument", err.Error(), nil))), nil
			}
			result, err := w.browser.Type(ctx, textArg(args, "selector"), text, boolArg(args, "clear"), boolArg(args, "submit"), secondsArg(args, "timeout", 15, 1, 60))
			if err != nil {
				return mcp.Text(pretty(errorPayload("browser_type_failed", err.Error(), map[string]any{"hint": browserInstructionHint(w.cdpEndpoint), "risk_note": browserControlRiskNote}))), nil
			}
			return mcp.Text(pretty(okPayload(map[string]any{"type": result}))), nil
		},
	}
}

func (w *WebTools) browserTabsTool() mcp.Tool {
	return mcp.Tool{
		Name:        "browser_tabs",
		Description: "browser_control: List, select, open, or close Chromium tabs/targets through the localhost CDP endpoint. Confirm before closing tabs involved in purchases, sends, deletes, account changes, or other consequential actions; refuses closing the last known page unless force=true.",
		InputSchema: schema(map[string]any{
			"action":    stringEnumProp("Tab action: list, select, open, or close.", "list", "select", "open", "close"),
			"target_id": stringProp("Target/tab id for select or close."),
			"url":       stringProp("URL for open. Defaults to about:blank when omitted."),
			"force":     boolProp("Allow closing the last known page target."),
			"timeout":   integerProp("Timeout in seconds. Defaults to 15 and is capped at 60."),
		}),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			result, err := w.browser.Tabs(ctx, textArg(args, "action"), textArg(args, "target_id"), textArg(args, "url"), boolArg(args, "force"), secondsArg(args, "timeout", 15, 1, 60))
			if err != nil {
				return mcp.Text(pretty(errorPayload("browser_tabs_failed", err.Error(), map[string]any{"hint": browserInstructionHint(w.cdpEndpoint), "risk_note": browserControlRiskNote}))), nil
			}
			return mcp.Text(pretty(okPayload(map[string]any{"tabs": result}))), nil
		},
	}
}

func (w *WebTools) browserEvalTool() mcp.Tool {
	return mcp.Tool{
		Name:        "browser_eval",
		Description: "browser_control: Evaluate a bounded JavaScript expression in the active tab only when --allow-eval=true. Disabled by default because JavaScript can read or mutate page state. Confirm before purchases, sends, deletes, account changes, private-data submission, or other consequential actions.",
		InputSchema: schema(map[string]any{
			"expression": stringProp("JavaScript expression to evaluate. Max 8192 bytes."),
			"timeout":    integerProp("Timeout in seconds. Defaults to 15 and is capped at 60."),
		}, "expression"),
		Handler: func(ctx context.Context, args map[string]any) (mcp.ToolResult, error) {
			if !w.allowEval {
				return mcp.Text(pretty(errorPayload("browser_eval_disabled", "browser_eval is disabled; restart nullbot-web-mcp with --allow-eval=true to enable bounded JavaScript evaluation", map[string]any{"allow_eval": false}))), nil
			}
			expression, err := requiredTextArg(args, "expression")
			if err != nil {
				return mcp.Text(pretty(errorPayload("invalid_argument", err.Error(), nil))), nil
			}
			result, err := w.browser.Eval(ctx, expression, secondsArg(args, "timeout", 15, 1, 60))
			if err != nil {
				return mcp.Text(pretty(errorPayload("browser_eval_failed", err.Error(), map[string]any{"hint": browserInstructionHint(w.cdpEndpoint)}))), nil
			}
			return mcp.Text(pretty(okPayload(map[string]any{"eval": result}))), nil
		},
	}
}

func numberArg(args map[string]any, key string, fallback float64) float64 {
	value, ok := args[key]
	if !ok || value == nil {
		return fallback
	}
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		parsed, err := v.Float64()
		if err != nil {
			return fallback
		}
		return parsed
	default:
		return fallback
	}
}

func interactionSafetyDescriptionsOK(tools []mcp.Tool) error {
	for _, tool := range tools {
		switch tool.Name {
		case "browser_click", "browser_type", "browser_tabs", "browser_eval":
			desc := strings.ToLower(tool.Description)
			if !strings.Contains(desc, "browser_control") || !strings.Contains(desc, "confirm") {
				return fmt.Errorf("%s description must mention browser_control and confirmation risk", tool.Name)
			}
			for _, term := range []string{"purchases", "sends", "deletes", "account", "consequential"} {
				if !strings.Contains(desc, term) {
					return fmt.Errorf("%s description must mention %s safety", tool.Name, term)
				}
			}
		}
	}
	return nil
}
