package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	cdptarget "github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const (
	browserTimeout       = 15 * time.Second
	browserStatusTimeout = 5 * time.Second
)

type CDPEndpoint struct {
	Raw                 string `json:"raw"`
	HTTPBase            string `json:"http_base,omitempty"`
	BrowserWebSocketURL string `json:"browser_websocket_url,omitempty"`
	Loopback            bool   `json:"loopback"`
}

type BrowserTarget struct {
	ID                   string `json:"id,omitempty"`
	Type                 string `json:"type,omitempty"`
	Title                string `json:"title,omitempty"`
	URL                  string `json:"url,omitempty"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl,omitempty"`
}

type cdpVersionResponse struct {
	Browser              string `json:"Browser,omitempty"`
	ProtocolVersion      string `json:"Protocol-Version,omitempty"`
	UserAgent            string `json:"User-Agent,omitempty"`
	V8Version            string `json:"V8-Version,omitempty"`
	WebKitVersion        string `json:"WebKit-Version,omitempty"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl,omitempty"`
}

type BrowserStatus struct {
	Endpoint               CDPEndpoint     `json:"endpoint"`
	Reachable              bool            `json:"reachable"`
	Attached               bool            `json:"attached"`
	ActiveTargetID         string          `json:"active_target_id,omitempty"`
	ActiveTarget           *BrowserTarget  `json:"active_target,omitempty"`
	CurrentURL             string          `json:"current_url,omitempty"`
	CurrentTitle           string          `json:"current_title,omitempty"`
	Targets                []BrowserTarget `json:"targets,omitempty"`
	Version                map[string]any  `json:"version,omitempty"`
	Launched               bool            `json:"launched"`
	LaunchPID              int             `json:"launch_pid,omitempty"`
	LaunchProfile          string          `json:"launch_profile_dir,omitempty"`
	LastError              string          `json:"last_error,omitempty"`
	InstructionHint        string          `json:"instruction_hint,omitempty"`
	ActionableInstructions []string        `json:"actionable_instructions,omitempty"`
}

type BrowserManager struct {
	mu   sync.Mutex
	opMu sync.Mutex

	endpoint string
	client   *http.Client

	allocatorCancel context.CancelFunc
	browserCtx      context.Context
	browserCancel   context.CancelFunc
	ctx             context.Context
	cancel          context.CancelFunc
	attached        bool
	activeTargetID  string
	lastError       string

	launched   *exec.Cmd
	profileDir string
}

func NewBrowserManager(endpoint string, client *http.Client) *BrowserManager {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = defaultCDPEndpoint
	}
	return &BrowserManager{
		endpoint: endpoint,
		client:   client,
	}
}

func (b *BrowserManager) Status(ctx context.Context, endpointOverride string) BrowserStatus {
	b.opMu.Lock()
	defer b.opMu.Unlock()

	b.mu.Lock()
	attached := b.attached
	activeTargetID := b.activeTargetID
	lastError := b.lastError
	launched := b.launched != nil && b.launched.Process != nil && b.launched.ProcessState == nil
	pid := 0
	if launched {
		pid = b.launched.Process.Pid
	}
	profileDir := b.profileDir
	browserCtx := b.ctx
	endpointValue := b.endpoint
	b.mu.Unlock()

	if strings.TrimSpace(endpointOverride) != "" {
		endpointValue = strings.TrimSpace(endpointOverride)
	}
	status := BrowserStatus{
		Attached:        attached,
		ActiveTargetID:  activeTargetID,
		Launched:        launched,
		LaunchPID:       pid,
		LaunchProfile:   profileDir,
		LastError:       lastError,
		InstructionHint: browserInstructionHint(endpointValue),
	}

	probeCtx, cancel := context.WithTimeout(ctx, browserStatusTimeout)
	defer cancel()
	endpoint, version, targets, err := b.probeEndpoint(probeCtx, endpointValue)
	status.Endpoint = endpoint
	status.Targets = targets
	status.Version = cdpVersionPayload(version)
	if status.ActiveTargetID == "" {
		status.ActiveTargetID = firstPageTargetID(targets)
	}
	fillActiveTarget(&status)
	if err != nil {
		status.Reachable = false
		status.LastError = err.Error()
		status.ActionableInstructions = browserActionableInstructions(endpointValue, status, err)
		return status
	}
	status.Reachable = true

	if attached && browserCtx != nil {
		runCtx, runCancel := context.WithTimeout(browserCtx, browserStatusTimeout)
		defer runCancel()
		var title, location string
		if err := chromedp.Run(runCtx, chromedp.Title(&title), chromedp.Location(&location)); err == nil {
			status.CurrentTitle = title
			status.CurrentURL = location
		}
	}
	fillActiveTarget(&status)
	status.ActionableInstructions = browserActionableInstructions(endpointValue, status, nil)
	return status
}

func (b *BrowserManager) Attach(ctx context.Context, endpointOverride string) (BrowserStatus, error) {
	b.opMu.Lock()
	defer b.opMu.Unlock()

	endpointValue := strings.TrimSpace(endpointOverride)
	if endpointValue == "" {
		endpointValue = b.endpoint
	}
	probeCtx, cancel := context.WithTimeout(ctx, browserTimeout)
	defer cancel()
	endpoint, version, targets, err := b.probeEndpoint(probeCtx, endpointValue)
	if err != nil {
		b.setLastError(err.Error())
		status := BrowserStatus{
			Endpoint:               endpoint,
			Reachable:              false,
			Attached:               false,
			Targets:                targets,
			Version:                cdpVersionPayload(version),
			LastError:              err.Error(),
			InstructionHint:        browserInstructionHint(endpointValue),
			ActionableInstructions: nil,
		}
		status.ActionableInstructions = browserActionableInstructions(endpointValue, status, err)
		return status, err
	}
	if endpoint.BrowserWebSocketURL == "" {
		err := fmt.Errorf("CDP endpoint did not expose a browser websocket URL; start Chromium with --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222")
		b.setLastError(err.Error())
		status := BrowserStatus{
			Endpoint:        endpoint,
			Reachable:       true,
			Attached:        false,
			ActiveTargetID:  firstPageTargetID(targets),
			Targets:         targets,
			Version:         cdpVersionPayload(version),
			LastError:       err.Error(),
			InstructionHint: browserInstructionHint(endpointValue),
		}
		fillActiveTarget(&status)
		status.ActionableInstructions = browserActionableInstructions(endpointValue, status, err)
		return status, err
	}

	b.mu.Lock()
	activeTargetID := activeTargetOrFirst(b.activeTargetID, targets)
	b.mu.Unlock()
	if activeTargetID == "" {
		err := fmt.Errorf("CDP endpoint did not report a page target; open a normal browser tab and retry browser_attach")
		b.setLastError(err.Error())
		status := BrowserStatus{
			Endpoint:        endpoint,
			Reachable:       true,
			Attached:        false,
			Targets:         targets,
			Version:         cdpVersionPayload(version),
			LastError:       err.Error(),
			InstructionHint: browserInstructionHint(endpointValue),
		}
		status.ActionableInstructions = browserActionableInstructions(endpointValue, status, err)
		return status, err
	}

	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), endpoint.BrowserWebSocketURL)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	if err := allocateRemoteBrowserWithTimeout(browserCtx, browserTimeout); err != nil {
		browserCancel()
		allocCancel()
		message := err.Error()
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(browserCtx.Err(), context.DeadlineExceeded) {
			message = fmt.Sprintf("browser_attach timed out after %s while connecting to the CDP browser websocket", browserTimeout)
		}
		b.setLastError(message)
		status := BrowserStatus{
			Endpoint:        endpoint,
			Reachable:       true,
			Attached:        false,
			ActiveTargetID:  activeTargetID,
			Targets:         targets,
			Version:         cdpVersionPayload(version),
			LastError:       message,
			InstructionHint: browserInstructionHint(endpointValue),
		}
		fillActiveTarget(&status)
		status.ActionableInstructions = browserActionableInstructions(endpointValue, status, err)
		return status, err
	}

	targetCtx, targetCancel := chromedp.NewContext(browserCtx, chromedp.WithTargetID(cdptarget.ID(activeTargetID)))
	var title, location string
	if err := runRootChromedpWithTimeout(targetCtx, browserTimeout, chromedp.Title(&title), chromedp.Location(&location)); err != nil {
		cancelChromedpContextWithoutClosingTarget(targetCtx, targetCancel)
		browserCancel()
		allocCancel()
		message := err.Error()
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(targetCtx.Err(), context.DeadlineExceeded) {
			message = fmt.Sprintf("browser_attach timed out after %s during CDP target initialization", browserTimeout)
		}
		b.setLastError(message)
		status := BrowserStatus{
			Endpoint:        endpoint,
			Reachable:       true,
			Attached:        false,
			ActiveTargetID:  activeTargetID,
			Targets:         targets,
			Version:         cdpVersionPayload(version),
			LastError:       message,
			InstructionHint: browserInstructionHint(endpointValue),
		}
		fillActiveTarget(&status)
		status.ActionableInstructions = browserActionableInstructions(endpointValue, status, err)
		return status, err
	}
	if c := chromedp.FromContext(targetCtx); c != nil && c.Target != nil && c.Target.TargetID != "" {
		activeTargetID = string(c.Target.TargetID)
	}

	refreshCtx, refreshCancel := context.WithTimeout(context.Background(), browserStatusTimeout)
	if refreshedEndpoint, refreshedVersion, refreshedTargets, refreshErr := b.probeEndpoint(refreshCtx, endpointValue); refreshErr == nil {
		endpoint = refreshedEndpoint
		version = refreshedVersion
		targets = refreshedTargets
	}
	refreshCancel()

	b.mu.Lock()
	b.closeLocked()
	b.allocatorCancel = allocCancel
	b.browserCtx = browserCtx
	b.browserCancel = browserCancel
	b.ctx = targetCtx
	b.cancel = targetCancel
	b.endpoint = endpointValue
	b.activeTargetID = activeTargetID
	b.attached = true
	b.lastError = ""
	b.mu.Unlock()

	status := BrowserStatus{
		Endpoint:        endpoint,
		Reachable:       true,
		Attached:        true,
		ActiveTargetID:  activeTargetID,
		CurrentTitle:    title,
		CurrentURL:      location,
		Targets:         targets,
		Version:         cdpVersionPayload(version),
		InstructionHint: browserInstructionHint(endpointValue),
	}
	fillActiveTarget(&status)
	status.ActionableInstructions = browserActionableInstructions(endpointValue, status, nil)
	return status, nil
}

func (b *BrowserManager) Detach() {
	b.opMu.Lock()
	defer b.opMu.Unlock()

	b.detachLockedByCaller()
}

func (b *BrowserManager) detachLockedByCaller() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeLocked()
}

func (b *BrowserManager) closeLocked() {
	if b.cancel != nil {
		cancelChromedpContextWithoutClosingTarget(b.ctx, b.cancel)
	}
	if b.browserCancel != nil {
		cancelChromedpContextWithoutClosingTarget(b.browserCtx, b.browserCancel)
	}
	if b.allocatorCancel != nil {
		b.allocatorCancel()
	}
	b.cancel = nil
	b.browserCancel = nil
	b.allocatorCancel = nil
	b.browserCtx = nil
	b.ctx = nil
	b.attached = false
	b.activeTargetID = ""
}

func (b *BrowserManager) setLastError(message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastError = message
}

func (b *BrowserManager) attachedContext(timeout time.Duration) (context.Context, context.CancelFunc, error) {
	b.mu.Lock()
	ctx := b.ctx
	attached := b.attached
	b.mu.Unlock()
	if !attached || ctx == nil {
		return nil, nil, errBrowserNotAttached()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	return ctx, cancel, nil
}

func allocateRemoteBrowserWithTimeout(ctx context.Context, timeout time.Duration) error {
	c := chromedp.FromContext(ctx)
	if c == nil || c.Allocator == nil {
		return fmt.Errorf("browser_attach: invalid chromedp browser context")
	}
	if c.Browser != nil {
		return nil
	}
	type allocateResult struct {
		err error
	}
	done := make(chan allocateResult, 1)
	go func() {
		browser, err := c.Allocator.Allocate(ctx)
		if err == nil {
			c.Browser = browser
		}
		done <- allocateResult{err: err}
	}()
	if timeout <= 0 {
		return (<-done).err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-done:
		return result.err
	case <-timer.C:
		return context.DeadlineExceeded
	case <-ctx.Done():
		return ctx.Err()
	}
}

func runRootChromedpWithTimeout(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
	if timeout <= 0 {
		return chromedp.Run(ctx, actions...)
	}
	done := make(chan error, 1)
	go func() {
		done <- chromedp.Run(ctx, actions...)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return context.DeadlineExceeded
	case <-ctx.Done():
		return ctx.Err()
	}
}

func cancelChromedpContextWithoutClosingTarget(ctx context.Context, cancel context.CancelFunc) {
	if ctx == nil || cancel == nil {
		return
	}
	if c := chromedp.FromContext(ctx); c != nil {
		c.Target = nil
	}
	cancel()
}

func (b *BrowserManager) bindActiveTarget(targetID string, timeout time.Duration) (string, string, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return "", "", fmt.Errorf("target_id: required")
	}
	if timeout <= 0 {
		timeout = browserTimeout
	}

	b.mu.Lock()
	browserCtx := b.browserCtx
	attached := b.attached
	b.mu.Unlock()
	if !attached || browserCtx == nil {
		return "", "", errBrowserNotAttached()
	}

	targetCtx, targetCancel := chromedp.NewContext(browserCtx, chromedp.WithTargetID(cdptarget.ID(targetID)))
	var title, location string
	if err := runRootChromedpWithTimeout(targetCtx, timeout, chromedp.Title(&title), chromedp.Location(&location)); err != nil {
		cancelChromedpContextWithoutClosingTarget(targetCtx, targetCancel)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(targetCtx.Err(), context.DeadlineExceeded) {
			return "", "", simpleError(fmt.Sprintf("browser_tabs select timed out after %s while attaching to target %s", timeout, targetID))
		}
		return "", "", err
	}

	b.mu.Lock()
	oldCtx := b.ctx
	oldCancel := b.cancel
	b.ctx = targetCtx
	b.cancel = targetCancel
	b.activeTargetID = targetID
	b.attached = true
	b.lastError = ""
	b.mu.Unlock()

	cancelChromedpContextWithoutClosingTarget(oldCtx, oldCancel)
	return title, location, nil
}

func (b *BrowserManager) Navigate(ctx context.Context, rawURL string, timeout time.Duration) (map[string]any, error) {
	b.opMu.Lock()
	defer b.opMu.Unlock()

	navURL, err := validateBrowserURL(rawURL)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = browserTimeout
	}
	runCtx, cancel, err := b.attachedContext(timeout)
	if err != nil {
		return nil, err
	}
	defer cancel()

	start := time.Now()
	var title, location, readyState string
	err = chromedp.Run(runCtx,
		chromedp.Navigate(navURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Evaluate(`document.readyState`, &readyState),
		chromedp.Title(&title),
		chromedp.Location(&location),
	)
	if err != nil {
		message := err.Error()
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			message = fmt.Sprintf("browser_navigate timed out after %s waiting for page readiness", timeout)
		}
		b.setLastError(message)
		return nil, simpleError(message)
	}
	return map[string]any{
		"requested_url": navURL,
		"url":           location,
		"title":         title,
		"ready_state":   readyState,
		"elapsed_ms":    time.Since(start).Milliseconds(),
		"timeout_ms":    timeout.Milliseconds(),
		"waited_for":    "body_ready",
	}, nil
}

func (b *BrowserManager) Read(ctx context.Context, selector, mode string, limit int, timeout time.Duration) (map[string]any, error) {
	b.opMu.Lock()
	defer b.opMu.Unlock()

	selector = strings.TrimSpace(selector)
	if selector == "" {
		selector = "body"
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "text"
	}
	if !validBrowserReadMode(mode) {
		return nil, fmt.Errorf("mode: expected text, readability, outline, or html")
	}
	limit = clampInt(limit, 1, maxReadabilityBytes)
	if timeout <= 0 {
		timeout = browserTimeout
	}
	runCtx, cancel, err := b.attachedContext(timeout)
	if err != nil {
		return nil, err
	}
	defer cancel()

	start := time.Now()
	var title, location string
	result := map[string]any{
		"selector":         selector,
		"mode":             mode,
		"source":           "active_tab",
		"timeout_ms":       timeout.Milliseconds(),
		"max_output_bytes": limit,
	}
	switch mode {
	case "text":
		var text string
		if err := chromedp.Run(runCtx, chromedp.Text(selector, &text, chromedp.ByQuery), chromedp.Title(&title), chromedp.Location(&location)); err != nil {
			message := browserReadErrorMessage(err, runCtx, timeout)
			b.setLastError(message)
			return nil, simpleError(message)
		}
		result["output_kind"] = "selected_element_text"
		for key, value := range cappedTextResult(text, limit) {
			result[key] = value
		}
	case "readability", "html", "outline":
		var html string
		if err := chromedp.Run(runCtx, chromedp.OuterHTML(selector, &html, chromedp.ByQuery), chromedp.Title(&title), chromedp.Location(&location)); err != nil {
			message := browserReadErrorMessage(err, runCtx, timeout)
			b.setLastError(message)
			return nil, simpleError(message)
		}
		var text string
		switch mode {
		case "html":
			result["output_kind"] = "selected_element_html"
			text = html
		case "outline":
			result["output_kind"] = "dom_outline"
			text = htmlToDOMOutline(strings.NewReader(html))
		case "readability":
			result["output_kind"] = "readability_text"
			text = htmlToReadableText(strings.NewReader(html))
		}
		result["html_bytes"] = len(html)
		for key, value := range cappedTextResult(text, limit) {
			result[key] = value
		}
	}
	result["url"] = location
	result["title"] = title
	result["elapsed_ms"] = time.Since(start).Milliseconds()
	return result, nil
}

func validBrowserReadMode(mode string) bool {
	switch mode {
	case "text", "readability", "outline", "html":
		return true
	default:
		return false
	}
}

func browserReadErrorMessage(err error, runCtx context.Context, timeout time.Duration) string {
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("browser_read timed out after %s waiting for page content", timeout)
	}
	return err.Error()
}

func (b *BrowserManager) Screenshot(ctx context.Context, fullPage bool, timeout time.Duration) ([]byte, error) {
	b.opMu.Lock()
	defer b.opMu.Unlock()

	if timeout <= 0 {
		timeout = browserTimeout
	}
	runCtx, cancel, err := b.attachedContext(timeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	var png []byte
	if fullPage {
		err = chromedp.Run(runCtx, chromedp.FullScreenshot(&png, 100))
	} else {
		err = chromedp.Run(runCtx, chromedp.CaptureScreenshot(&png))
	}
	if err != nil {
		message := err.Error()
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			message = fmt.Sprintf("browser_screenshot timed out after %s waiting for PNG capture", timeout)
		}
		b.setLastError(message)
		return nil, simpleError(message)
	}
	if len(png) == 0 {
		message := "browser_screenshot produced an empty PNG"
		b.setLastError(message)
		return nil, simpleError(message)
	}
	return png, nil
}

func (b *BrowserManager) Launch(ctx context.Context, browserPath string, port int, profileDir string, temporaryProfile bool) (map[string]any, error) {
	b.opMu.Lock()
	defer b.opMu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("browser_launch canceled before start: %w", err)
	}
	port, err := launchPort(port, b.endpoint)
	if err != nil {
		return nil, err
	}
	path, err := resolveBrowserPath(browserPath)
	if err != nil {
		return nil, err
	}
	profile, err := prepareBrowserProfile(profileDir, temporaryProfile)
	if err != nil {
		return nil, err
	}
	cleanupOnFailure := func() {
		if profile.CreatedTemp {
			_ = os.RemoveAll(profile.Dir)
		}
	}

	b.mu.Lock()
	if b.launched != nil && b.launched.Process != nil && b.launched.ProcessState == nil {
		pid := b.launched.Process.Pid
		b.mu.Unlock()
		cleanupOnFailure()
		return nil, fmt.Errorf("browser_launch: a browser process launched by this server already appears to be running (pid %d); use browser_status/browser_attach or restart the MCP server before launching another", pid)
	}
	b.mu.Unlock()

	args := browserLaunchArgs(port, profile.Dir)
	cmd := exec.Command(path, args...)
	if err := cmd.Start(); err != nil {
		cleanupOnFailure()
		return nil, fmt.Errorf("launch browser: %w", err)
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)

	b.mu.Lock()
	b.launched = cmd
	b.profileDir = profile.Dir
	b.endpoint = endpoint
	b.lastError = ""
	b.mu.Unlock()

	go b.waitLaunchedProcess(cmd, profile)

	ready := false
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			lastErr = err
			break
		}
		probeCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		_, _, _, lastErr = b.probeEndpoint(probeCtx, endpoint)
		cancel()
		if lastErr == nil {
			ready = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	payload := map[string]any{
		"launched":                 true,
		"pid":                      cmd.Process.Pid,
		"browser_path":             path,
		"cdp_endpoint":             endpoint,
		"profile_dir":              profile.Dir,
		"profile_strategy":         profile.Strategy,
		"temporary_profile":        profile.CreatedTemp,
		"remote_debugging_address": "127.0.0.1",
		"remote_debugging_port":    port,
		"args":                     args,
		"ready":                    ready,
		"risk_note":                browserLaunchRiskNote(profile),
		"next_steps": []string{
			"If ready is true, call browser_attach to connect to this local CDP endpoint.",
			"Use the launched isolated profile for automation when possible; avoid consequential actions unless explicitly confirmed by the user.",
		},
	}
	if lastErr != nil && !ready {
		payload["last_probe_error"] = lastErr.Error()
		payload["next_steps"] = []string{
			"The browser process was started, but the CDP endpoint was not ready before the readiness timeout.",
			"Wait a moment, then call browser_status and browser_attach for " + endpoint + ".",
			"If status remains unreachable, confirm the browser supports --remote-debugging-address and --remote-debugging-port.",
		}
	}
	return payload, nil
}

func (b *BrowserManager) waitLaunchedProcess(cmd *exec.Cmd, profile browserProfile) {
	err := cmd.Wait()
	if profile.CreatedTemp {
		_ = os.RemoveAll(profile.Dir)
	}
	b.mu.Lock()
	if b.launched == cmd {
		if err != nil {
			b.lastError = "launched browser exited: " + err.Error()
		}
	}
	b.mu.Unlock()
}

type browserProfile struct {
	Dir         string
	CreatedTemp bool
	Strategy    string
}

func launchPort(requested int, endpoint string) (int, error) {
	port := requested
	if port <= 0 {
		port = portFromEndpoint(endpoint)
	}
	if port <= 0 {
		port = 9222
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port: expected 1..65535, got %d", port)
	}
	return port, nil
}

func prepareBrowserProfile(profileDir string, temporaryProfile bool) (browserProfile, error) {
	profileDir = strings.TrimSpace(profileDir)
	if profileDir == "" || temporaryProfile {
		dir, err := os.MkdirTemp("", "nullbot-web-mcp-profile-*")
		if err != nil {
			return browserProfile{}, fmt.Errorf("create temporary browser profile: %w", err)
		}
		return browserProfile{Dir: dir, CreatedTemp: true, Strategy: "temporary_isolated"}, nil
	}
	abs, err := filepath.Abs(profileDir)
	if err != nil {
		return browserProfile{}, fmt.Errorf("profile_dir: resolve absolute path: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return browserProfile{}, fmt.Errorf("create browser profile directory: %w", err)
	}
	return browserProfile{Dir: abs, Strategy: "provided_user_data_dir"}, nil
}

func browserLaunchArgs(port int, profileDir string) []string {
	return []string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + strconv.Itoa(port),
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"about:blank",
	}
}

func browserLaunchRiskNote(profile browserProfile) string {
	base := "Browser automation can read pages and perform clicks/typing in the launched browser. Confirm consequential actions before sending messages, making purchases, deleting data, changing account settings, or submitting private information. CDP is bound to 127.0.0.1 only."
	if profile.CreatedTemp {
		return base + " This launch uses an isolated temporary profile by default, which reduces risk of acting in the user's real logged-in browser profile."
	}
	return base + " This launch uses a caller-provided user-data-dir/profile; pages may retain cookies, sessions, history, downloads, and account state in that directory."
}
func (b *BrowserManager) probeEndpoint(ctx context.Context, endpointValue string) (CDPEndpoint, *cdpVersionResponse, []BrowserTarget, error) {
	endpoint, err := normalizeCDPEndpoint(endpointValue)
	if err != nil {
		return endpoint, nil, nil, err
	}
	client := b.client
	if client == nil {
		client = &http.Client{Timeout: browserStatusTimeout}
	}
	var version *cdpVersionResponse
	var targets []BrowserTarget
	if endpoint.HTTPBase != "" {
		version, err = fetchCDPVersion(ctx, client, endpoint.HTTPBase)
		if err != nil {
			return endpoint, nil, nil, err
		}
		if endpoint.BrowserWebSocketURL == "" {
			endpoint.BrowserWebSocketURL = version.WebSocketDebuggerURL
		}
		targets, _ = fetchCDPTargets(ctx, client, endpoint.HTTPBase)
	}
	return endpoint, version, targets, nil
}

func normalizeCDPEndpoint(value string) (CDPEndpoint, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		raw = defaultCDPEndpoint
	}
	if strings.HasPrefix(raw, "//") {
		raw = "http:" + raw
	} else if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return CDPEndpoint{Raw: raw}, fmt.Errorf("cdp_endpoint: parse: %w", err)
	}
	if u.Host == "" {
		return CDPEndpoint{Raw: raw}, fmt.Errorf("cdp_endpoint: host is required")
	}
	if !isLoopbackHost(u.Hostname()) {
		return CDPEndpoint{Raw: raw}, fmt.Errorf("cdp_endpoint: only localhost/127.0.0.1/[::1] endpoints are allowed, got %s", u.Hostname())
	}
	out := CDPEndpoint{Raw: raw, Loopback: true}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		u.Path = strings.TrimRight(u.Path, "/")
		u.RawQuery = ""
		u.Fragment = ""
		if u.Path == "" {
			u.Path = ""
		}
		out.HTTPBase = strings.TrimRight(u.String(), "/")
	case "ws", "wss":
		out.BrowserWebSocketURL = u.String()
		if u.Scheme == "ws" {
			u.Scheme = "http"
		} else {
			u.Scheme = "https"
		}
		u.Path = ""
		u.RawQuery = ""
		u.Fragment = ""
		out.HTTPBase = strings.TrimRight(u.String(), "/")
	default:
		return out, fmt.Errorf("cdp_endpoint: unsupported scheme %q; use http(s) or ws(s)", u.Scheme)
	}
	return out, nil
}

func fetchCDPVersion(ctx context.Context, client *http.Client, httpBase string) (*cdpVersionResponse, error) {
	var version cdpVersionResponse
	if err := getJSON(ctx, client, strings.TrimRight(httpBase, "/")+"/json/version", &version); err != nil {
		return nil, fmt.Errorf("probe CDP /json/version: %w", err)
	}
	return &version, nil
}

func fetchCDPTargets(ctx context.Context, client *http.Client, httpBase string) ([]BrowserTarget, error) {
	var targets []BrowserTarget
	if err := getJSON(ctx, client, strings.TrimRight(httpBase, "/")+"/json/list", &targets); err != nil {
		return nil, fmt.Errorf("probe CDP /json/list: %w", err)
	}
	return targets, nil
}

func getJSON(ctx context.Context, client *http.Client, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return err
	}
	return nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func firstPageTargetID(targets []BrowserTarget) string {
	for _, target := range targets {
		if strings.EqualFold(target.Type, "page") && strings.TrimSpace(target.ID) != "" {
			return target.ID
		}
	}
	for _, target := range targets {
		if strings.TrimSpace(target.ID) != "" {
			return target.ID
		}
	}
	return ""
}

func validateBrowserURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("url: required")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("url: parse: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("url: only http and https URLs are allowed")
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", fmt.Errorf("url: host is required")
	}
	if u.User != nil {
		return "", fmt.Errorf("url: embedded credentials are not allowed")
	}
	u.Scheme = scheme
	u.Fragment = ""
	return u.String(), nil
}

func portFromEndpoint(endpoint string) int {
	parsed, err := normalizeCDPEndpoint(endpoint)
	if err != nil || parsed.HTTPBase == "" {
		return 0
	}
	u, err := url.Parse(parsed.HTTPBase)
	if err != nil {
		return 0
	}
	port := u.Port()
	if port == "" {
		return 0
	}
	value, _ := strconv.Atoi(port)
	return value
}

func resolveBrowserPath(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return configured, nil
		}
		if found, err := exec.LookPath(configured); err == nil {
			return found, nil
		}
		return "", fmt.Errorf("browser_path: executable not found: %s", configured)
	}
	for _, candidate := range browserCandidates() {
		if found, err := exec.LookPath(candidate); err == nil {
			return found, nil
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("browser executable not found; pass --browser-path or browser_path. Tried common Chrome, Edge, and Brave locations")
}

func browserCandidates() []string {
	if runtime.GOOS != "windows" {
		return []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge", "brave-browser", "brave"}
	}
	var out []string
	out = append(out, "chrome.exe", "msedge.exe", "brave.exe")
	roots := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")}
	for _, root := range roots {
		if root == "" {
			continue
		}
		out = append(out,
			filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(root, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
		)
	}
	return out
}

func cdpVersionPayload(version *cdpVersionResponse) map[string]any {
	if version == nil {
		return nil
	}
	return map[string]any{
		"browser":          version.Browser,
		"protocol_version": version.ProtocolVersion,
		"user_agent":       version.UserAgent,
		"v8_version":       version.V8Version,
		"webkit_version":   version.WebKitVersion,
	}
}

func fillActiveTarget(status *BrowserStatus) {
	if status == nil {
		return
	}
	if status.ActiveTargetID == "" {
		status.ActiveTargetID = firstPageTargetID(status.Targets)
	}
	for i := range status.Targets {
		target := status.Targets[i]
		if status.ActiveTargetID != "" && target.ID == status.ActiveTargetID {
			status.ActiveTarget = &target
			if status.CurrentURL == "" {
				status.CurrentURL = target.URL
			}
			if status.CurrentTitle == "" {
				status.CurrentTitle = target.Title
			}
			return
		}
	}
	if status.ActiveTarget == nil && len(status.Targets) > 0 && status.ActiveTargetID == "" {
		target := status.Targets[0]
		status.ActiveTargetID = target.ID
		status.ActiveTarget = &target
		if status.CurrentURL == "" {
			status.CurrentURL = target.URL
		}
		if status.CurrentTitle == "" {
			status.CurrentTitle = target.Title
		}
	}
}

func browserActionableInstructions(endpointValue string, status BrowserStatus, err error) []string {
	endpointValue = strings.TrimSpace(endpointValue)
	if endpointValue == "" {
		endpointValue = defaultCDPEndpoint
	}
	instructions := []string{}
	if status.Reachable && status.Endpoint.BrowserWebSocketURL == "" {
		return []string{
			"The HTTP endpoint responded, but /json/version did not include webSocketDebuggerUrl.",
			"Confirm this is a Chromium DevTools Protocol endpoint, not an ordinary web server.",
			"Restart Chromium with --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 and retry browser_attach.",
		}
	}
	if err != nil || !status.Reachable {
		instructions = append(instructions,
			"Ensure Chrome/Edge/Brave/Chromium is running with remote debugging bound to localhost only.",
			"Example: chrome --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 --user-data-dir=<temporary-profile>",
			"Then retry browser_attach with endpoint "+endpointValue+".",
		)
		if strings.Contains(endpointValue, ":1") || strings.Contains(endpointValue, ":0") {
			instructions = append(instructions, "The configured endpoint port looks unusual; the default CDP port is 9222.")
		}
		return instructions
	}
	if !status.Attached {
		instructions = append(instructions, "Endpoint is reachable. Call browser_attach to connect to the active Chromium tab.")
	}
	if len(status.Targets) == 0 {
		instructions = append(instructions, "No page targets were reported by /json/list; open a normal browser tab and retry browser_status.")
	} else if status.ActiveTargetID == "" {
		instructions = append(instructions, "Targets were reported, but no active page target was selected; open or focus a normal browser tab and retry.")
	}
	if status.Attached {
		instructions = append(instructions, "Browser is attached. You can use browser_navigate, browser_read, and browser_screenshot on the active tab.")
	}
	return instructions
}

func browserInstructionHint(endpoint string) string {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = defaultCDPEndpoint
	}
	return "Start Chromium/Chrome/Edge/Brave with a localhost CDP endpoint, for example: chrome --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 --user-data-dir=<temporary-profile>, then call browser_attach. Configured endpoint: " + endpoint
}

func errBrowserNotAttached() error {
	return simpleError("browser is not attached; call browser_status for diagnostics, then start Chromium with --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 and call browser_attach")
}
