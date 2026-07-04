package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

const browserControlRiskNote = "browser_control risk: this tool can interact with pages in a real browser. Confirm before purchases, sends, deletes, account changes, credential entry, private-data submission, or other consequential actions."

func (b *BrowserManager) Query(ctx context.Context, selector string, limit int, timeout time.Duration) (map[string]any, error) {
	b.opMu.Lock()
	defer b.opMu.Unlock()

	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, fmt.Errorf("selector: required CSS selector")
	}
	limit = clampInt(limit, 1, 100)
	if timeout <= 0 {
		timeout = browserTimeout
	}
	runCtx, cancel, err := b.attachedContext(timeout)
	if err != nil {
		return nil, err
	}
	defer cancel()

	selectorJSON, _ := json.Marshal(selector)
	script := fmt.Sprintf(`(() => {
  const selector = %s;
  const limit = %d;
  const nodes = Array.from(document.querySelectorAll(selector));
  return nodes.slice(0, limit).map((el, i) => {
    const rect = el.getBoundingClientRect();
    const style = window.getComputedStyle(el);
    const attrs = {};
    for (const name of ['id','class','name','type','role','aria-label','href','src','title','alt','placeholder','value']) {
      if (el.hasAttribute && el.hasAttribute(name)) attrs[name] = el.getAttribute(name);
    }
    return {
      index: i,
      tag: (el.tagName || '').toLowerCase(),
      text: (el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 1000),
      attributes: attrs,
      visible: !!(rect.width || rect.height) && style.visibility !== 'hidden' && style.display !== 'none',
      disabled: !!el.disabled,
      checked: !!el.checked,
      bounding_box: {x: rect.x, y: rect.y, width: rect.width, height: rect.height}
    };
  });
})()`, selectorJSON, limit)
	countScript := fmt.Sprintf(`document.querySelectorAll(%s).length`, selectorJSON)

	started := time.Now()
	var matches []map[string]any
	var total int
	var title, location string
	if err := chromedp.Run(runCtx,
		chromedp.Evaluate(countScript, &total),
		chromedp.Evaluate(script, &matches),
		chromedp.Title(&title),
		chromedp.Location(&location),
	); err != nil {
		message := browserInteractionErrorMessage("browser_query", err, runCtx, timeout)
		b.setLastError(message)
		return nil, simpleError(message)
	}
	return map[string]any{
		"selector":      selector,
		"matches":       matches,
		"match_count":   len(matches),
		"total_matches": total,
		"truncated":     total > len(matches),
		"limit":         limit,
		"url":           location,
		"title":         title,
		"elapsed_ms":    time.Since(started).Milliseconds(),
		"timeout_ms":    timeout.Milliseconds(),
	}, nil
}

func (b *BrowserManager) Click(ctx context.Context, selector string, x, y float64, timeout time.Duration) (map[string]any, error) {
	b.opMu.Lock()
	defer b.opMu.Unlock()

	selector = strings.TrimSpace(selector)
	usingCoordinates := selector == "" && (x != 0 || y != 0)
	if selector == "" && !usingCoordinates {
		return nil, fmt.Errorf("selector or non-zero x/y coordinates are required")
	}
	if timeout <= 0 {
		timeout = browserTimeout
	}
	runCtx, cancel, err := b.attachedContext(timeout)
	if err != nil {
		return nil, err
	}
	defer cancel()

	started := time.Now()
	var title, location string
	if selector != "" {
		selectorJSON, _ := json.Marshal(selector)
		var count int
		if err := chromedp.Run(runCtx, chromedp.Evaluate(fmt.Sprintf(`document.querySelectorAll(%s).length`, selectorJSON), &count)); err != nil {
			message := browserInteractionErrorMessage("browser_click", err, runCtx, timeout)
			b.setLastError(message)
			return nil, simpleError(message)
		}
		if count == 0 {
			return nil, fmt.Errorf("browser_click: no elements match selector %q", selector)
		}
		if count > 1 {
			return nil, fmt.Errorf("browser_click: selector %q matched %d elements; use a more specific selector", selector, count)
		}
		if err := chromedp.Run(runCtx, chromedp.Click(selector, chromedp.ByQuery), chromedp.Title(&title), chromedp.Location(&location)); err != nil {
			message := browserInteractionErrorMessage("browser_click", err, runCtx, timeout)
			b.setLastError(message)
			return nil, simpleError(message)
		}
		return map[string]any{"clicked": true, "method": "selector", "selector": selector, "url": location, "title": title, "elapsed_ms": time.Since(started).Milliseconds(), "timeout_ms": timeout.Milliseconds(), "risk_note": browserControlRiskNote}, nil
	}

	script := fmt.Sprintf(`(() => { const el = document.elementFromPoint(%f, %f); if (!el) return {clicked:false, error:'no element at coordinates'}; el.click(); const r = el.getBoundingClientRect(); return {clicked:true, tag:(el.tagName||'').toLowerCase(), text:(el.innerText||el.textContent||'').replace(/\s+/g,' ').trim().slice(0,200), bounding_box:{x:r.x,y:r.y,width:r.width,height:r.height}}; })()`, x, y)
	var info map[string]any
	if err := chromedp.Run(runCtx, chromedp.Evaluate(script, &info), chromedp.Title(&title), chromedp.Location(&location)); err != nil {
		message := browserInteractionErrorMessage("browser_click", err, runCtx, timeout)
		b.setLastError(message)
		return nil, simpleError(message)
	}
	if clicked, _ := info["clicked"].(bool); !clicked {
		return nil, fmt.Errorf("browser_click: %v", info["error"])
	}
	info["method"] = "coordinates"
	info["x"] = x
	info["y"] = y
	info["url"] = location
	info["title"] = title
	info["elapsed_ms"] = time.Since(started).Milliseconds()
	info["timeout_ms"] = timeout.Milliseconds()
	info["risk_note"] = browserControlRiskNote
	return info, nil
}

func (b *BrowserManager) Type(ctx context.Context, selector, text string, clear, submit bool, timeout time.Duration) (map[string]any, error) {
	b.opMu.Lock()
	defer b.opMu.Unlock()

	selector = strings.TrimSpace(selector)
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("text: required string")
	}
	if timeout <= 0 {
		timeout = browserTimeout
	}
	runCtx, cancel, err := b.attachedContext(timeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	started := time.Now()
	var title, location string
	keys := text
	if submit {
		keys += "\n"
	}
	if selector != "" {
		selectorJSON, _ := json.Marshal(selector)
		var count int
		if err := chromedp.Run(runCtx, chromedp.Evaluate(fmt.Sprintf(`document.querySelectorAll(%s).length`, selectorJSON), &count)); err != nil {
			message := browserInteractionErrorMessage("browser_type", err, runCtx, timeout)
			b.setLastError(message)
			return nil, simpleError(message)
		}
		if count == 0 {
			return nil, fmt.Errorf("browser_type: no elements match selector %q", selector)
		}
		if count > 1 {
			return nil, fmt.Errorf("browser_type: selector %q matched %d elements; use a more specific selector", selector, count)
		}
		actions := []chromedp.Action{chromedp.Focus(selector, chromedp.ByQuery)}
		if clear {
			actions = append(actions, chromedp.SetValue(selector, "", chromedp.ByQuery))
		}
		actions = append(actions, chromedp.SendKeys(selector, keys, chromedp.ByQuery), chromedp.Title(&title), chromedp.Location(&location))
		if err := chromedp.Run(runCtx, actions...); err != nil {
			message := browserInteractionErrorMessage("browser_type", err, runCtx, timeout)
			b.setLastError(message)
			return nil, simpleError(message)
		}
	} else {
		if err := chromedp.Run(runCtx, chromedp.KeyEvent(keys), chromedp.Title(&title), chromedp.Location(&location)); err != nil {
			message := browserInteractionErrorMessage("browser_type", err, runCtx, timeout)
			b.setLastError(message)
			return nil, simpleError(message)
		}
	}
	return map[string]any{
		"typed":      true,
		"selector":   selector,
		"text_bytes": len(text),
		"clear":      clear,
		"submit":     submit,
		"url":        location,
		"title":      title,
		"elapsed_ms": time.Since(started).Milliseconds(),
		"timeout_ms": timeout.Milliseconds(),
		"risk_note":  browserControlRiskNote,
	}, nil
}

func (b *BrowserManager) Eval(ctx context.Context, expression string, timeout time.Duration) (map[string]any, error) {
	b.opMu.Lock()
	defer b.opMu.Unlock()

	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil, fmt.Errorf("expression: required JavaScript expression")
	}
	if len(expression) > 8192 {
		return nil, fmt.Errorf("expression: too large; max 8192 bytes")
	}
	if timeout <= 0 {
		timeout = browserTimeout
	}
	runCtx, cancel, err := b.attachedContext(timeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	started := time.Now()
	var value any
	var title, location string
	if err := chromedp.Run(runCtx, chromedp.Evaluate(expression, &value), chromedp.Title(&title), chromedp.Location(&location)); err != nil {
		message := browserInteractionErrorMessage("browser_eval", err, runCtx, timeout)
		b.setLastError(message)
		return nil, simpleError(message)
	}
	return map[string]any{
		"evaluated":  true,
		"result":     value,
		"url":        location,
		"title":      title,
		"elapsed_ms": time.Since(started).Milliseconds(),
		"timeout_ms": timeout.Milliseconds(),
		"risk_note":  "JavaScript evaluation can read or change page state. It is disabled unless --allow-eval=true.",
	}, nil
}

func browserInteractionErrorMessage(tool string, err error, runCtx context.Context, timeout time.Duration) string {
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("%s timed out after %s", tool, timeout)
	}
	return err.Error()
}
