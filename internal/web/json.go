package web

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const defaultTextLimit = 32 * 1024

func pretty(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(data)
}

func textArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func requiredTextArg(args map[string]any, key string) (string, error) {
	value, ok := args[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s: required string", key)
	}
	return value, nil
}

func boolArg(args map[string]any, key string) bool {
	value, _ := args[key].(bool)
	return value
}

func intArg(args map[string]any, key string, fallback int) int {
	value, ok := args[key]
	if !ok || value == nil {
		return fallback
	}
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fallback
		}
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		parsed, err := v.Int64()
		if err != nil {
			return fallback
		}
		return int(parsed)
	default:
		return fallback
	}
}

func intArgRange(args map[string]any, key string, fallback, min, max int) int {
	value := intArg(args, key, fallback)
	return clampInt(value, min, max)
}

func stringSliceArg(args map[string]any, key string) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if value, ok := item.(string); ok {
			out = append(out, value)
		}
	}
	return out
}

func stringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func stringEnumProp(description string, values ...string) map[string]any {
	prop := stringProp(description)
	if len(values) > 0 {
		prop["enum"] = values
	}
	return prop
}

func numberProp(description string) map[string]any {
	return map[string]any{"type": "number", "description": description}
}

func integerProp(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func boolProp(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func arrayStringProp(description string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": description,
		"items":       map[string]any{"type": "string"},
	}
}

func schema(properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	if required == nil {
		required = []string{}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

func errorPayload(code, message string, details map[string]any) map[string]any {
	payload := map[string]any{
		"ok":      false,
		"code":    code,
		"message": message,
	}
	if details != nil {
		payload["details"] = details
	}
	return payload
}

func okPayload(fields map[string]any) map[string]any {
	payload := map[string]any{"ok": true}
	for key, value := range fields {
		payload[key] = value
	}
	return payload
}

func truncate(text string, limit int) string {
	return truncateText(text, limit)
}

func truncateText(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	if len(text) <= limit {
		return text
	}
	suffix := "\n...[truncated]"
	if limit <= len(suffix) {
		return safePrefix(text, limit)
	}
	prefix := safePrefix(text, limit-len(suffix))
	return strings.TrimRight(prefix, "\r\n") + suffix
}

func cappedTextResult(text string, limit int) map[string]any {
	if limit <= 0 {
		limit = defaultTextLimit
	}
	truncated := len(text) > limit
	return map[string]any{
		"text":             truncateText(text, limit),
		"bytes":            len(text),
		"truncated":        truncated,
		"max_output_bytes": limit,
	}
}

func safePrefix(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	prefix := text[:limit]
	for !utf8.ValidString(prefix) && len(prefix) > 0 {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
}

func clampInt(value, min, max int) int {
	if min > max {
		min, max = max, min
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
