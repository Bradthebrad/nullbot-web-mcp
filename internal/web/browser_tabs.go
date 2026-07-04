package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (b *BrowserManager) Tabs(ctx context.Context, action, targetID, rawURL string, force bool, timeout time.Duration) (map[string]any, error) {
	b.opMu.Lock()
	defer b.opMu.Unlock()

	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		action = "list"
	}
	if timeout <= 0 {
		timeout = browserTimeout
	}
	b.mu.Lock()
	endpointValue := b.endpoint
	attached := b.attached
	activeTargetID := b.activeTargetID
	b.mu.Unlock()

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	endpoint, version, targets, err := b.probeEndpoint(probeCtx, endpointValue)
	cancel()
	if err != nil {
		b.setLastError(err.Error())
		return nil, err
	}

	result := map[string]any{
		"action":           action,
		"endpoint":         endpoint,
		"targets":          targets,
		"target_count":     len(targets),
		"active_target_id": activeTargetOrFirst(activeTargetID, targets),
		"version":          cdpVersionPayload(version),
		"attached":         attached,
		"timeout_ms":       timeout.Milliseconds(),
	}

	switch action {
	case "list":
		return result, nil
	case "select":
		targetID = strings.TrimSpace(targetID)
		if targetID == "" {
			return nil, fmt.Errorf("target_id: required for browser_tabs action=select")
		}
		if !targetExists(targets, targetID) {
			return nil, fmt.Errorf("browser_tabs: target_id %q not found", targetID)
		}
		var title, location string
		if attached {
			var err error
			title, location, err = b.bindActiveTarget(targetID, timeout)
			if err != nil {
				b.setLastError(err.Error())
				return nil, fmt.Errorf("browser_tabs select: %w", err)
			}
		} else {
			b.mu.Lock()
			b.activeTargetID = targetID
			b.mu.Unlock()
		}
		result["selected"] = true
		result["active_target_id"] = targetID
		result["attached_to_target"] = attached
		if title != "" || location != "" {
			result["title"] = title
			result["url"] = location
		}
		return result, nil
	case "open":
		if strings.TrimSpace(rawURL) == "" {
			rawURL = "about:blank"
		}
		openURL := rawURL
		if strings.TrimSpace(rawURL) != "about:blank" {
			validated, err := validateBrowserURL(rawURL)
			if err != nil {
				return nil, err
			}
			openURL = validated
		}
		created, err := b.cdpNewTab(ctx, endpoint.HTTPBase, openURL, timeout)
		if err != nil {
			b.setLastError(err.Error())
			return nil, err
		}
		if created.ID != "" {
			if attached {
				title, location, err := b.bindActiveTarget(created.ID, timeout)
				if err != nil {
					b.setLastError(err.Error())
					return nil, fmt.Errorf("browser_tabs open: attach new target: %w", err)
				}
				result["title"] = title
				result["url"] = location
			} else {
				b.mu.Lock()
				b.activeTargetID = created.ID
				b.mu.Unlock()
			}
		}
		result["opened"] = true
		result["opened_target"] = created
		result["active_target_id"] = created.ID
		result["attached_to_target"] = attached && created.ID != ""
		result["risk_note"] = browserControlRiskNote
		return result, nil
	case "close":
		targetID = strings.TrimSpace(targetID)
		if targetID == "" {
			return nil, fmt.Errorf("target_id: required for browser_tabs action=close")
		}
		if !targetExists(targets, targetID) {
			return nil, fmt.Errorf("browser_tabs: target_id %q not found", targetID)
		}
		pageCount := countPageTargets(targets)
		if pageCount <= 1 && !force {
			return nil, fmt.Errorf("browser_tabs: refusing to close the last known page target without force=true")
		}
		if err := b.cdpCloseTab(ctx, endpoint.HTTPBase, targetID, timeout); err != nil {
			b.setLastError(err.Error())
			return nil, err
		}
		b.mu.Lock()
		if b.activeTargetID == targetID {
			b.activeTargetID = ""
		}
		b.mu.Unlock()
		result["closed"] = true
		result["closed_target_id"] = targetID
		result["risk_note"] = browserControlRiskNote
		return result, nil
	default:
		return nil, fmt.Errorf("action: expected list, select, open, or close")
	}
}

func (b *BrowserManager) cdpNewTab(ctx context.Context, httpBase, rawURL string, timeout time.Duration) (BrowserTarget, error) {
	if httpBase == "" {
		return BrowserTarget{}, fmt.Errorf("browser_tabs: CDP HTTP endpoint is unavailable")
	}
	var target BrowserTarget
	endpoint := strings.TrimRight(httpBase, "/") + "/json/new?" + urlQueryEscape(rawURL)
	if err := postOrPutCDP(ctx, b.client, http.MethodPut, endpoint, timeout, &target); err != nil {
		return BrowserTarget{}, fmt.Errorf("browser_tabs open: %w", err)
	}
	return target, nil
}

func (b *BrowserManager) cdpCloseTab(ctx context.Context, httpBase, targetID string, timeout time.Duration) error {
	if httpBase == "" {
		return fmt.Errorf("browser_tabs: CDP HTTP endpoint is unavailable")
	}
	endpoint := strings.TrimRight(httpBase, "/") + "/json/close/" + targetID
	if err := postOrPutCDP(ctx, b.client, http.MethodGet, endpoint, timeout, nil); err != nil {
		return fmt.Errorf("browser_tabs close: %w", err)
	}
	return nil
}

func postOrPutCDP(ctx context.Context, client *http.Client, method, rawURL string, timeout time.Duration, out any) error {
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "nullbot-web-mcp/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func targetExists(targets []BrowserTarget, targetID string) bool {
	for _, target := range targets {
		if target.ID == targetID {
			return true
		}
	}
	return false
}

func countPageTargets(targets []BrowserTarget) int {
	count := 0
	for _, target := range targets {
		if strings.EqualFold(target.Type, "page") {
			count++
		}
	}
	return count
}

func activeTargetOrFirst(activeTargetID string, targets []BrowserTarget) string {
	activeTargetID = strings.TrimSpace(activeTargetID)
	if activeTargetID != "" && targetExists(targets, activeTargetID) {
		return activeTargetID
	}
	return firstPageTargetID(targets)
}

func urlQueryEscape(value string) string {
	return url.QueryEscape(value)
}
