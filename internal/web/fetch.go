package web

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	fetchTimeout        = 20 * time.Second
	maxRedirects        = 5
	defaultOutputBytes  = 64 * 1024
	maxReadabilityBytes = 128 * 1024
)

type fetchResponse struct {
	URL             string         `json:"url"`
	FinalURL        string         `json:"final_url"`
	StatusCode      int            `json:"status_code"`
	Status          string         `json:"status"`
	ContentType     string         `json:"content_type,omitempty"`
	MediaType       string         `json:"media_type,omitempty"`
	Headers         map[string]any `json:"headers,omitempty"`
	BytesRead       int            `json:"bytes_read"`
	MaxFetchBytes   int64          `json:"max_fetch_bytes"`
	FetchTruncated  bool           `json:"fetch_truncated"`
	Text            string         `json:"text,omitempty"`
	TextBytes       int            `json:"text_bytes,omitempty"`
	TextTruncated   bool           `json:"text_truncated,omitempty"`
	ReadabilityText string         `json:"readability_text,omitempty"`
}

func (w *WebTools) fetchURL(ctx context.Context, rawURL string, outputMode string, outputLimit int) (fetchResponse, error) {
	validated, err := validateFetchURL(rawURL)
	if err != nil {
		return fetchResponse{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	client := w.fetchHTTPClient()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, validated.String(), nil)
	if err != nil {
		return fetchResponse{}, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/json;q=0.9,*/*;q=0.5")
	req.Header.Set("User-Agent", "nullbot-web-mcp/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return fetchResponse{}, err
	}
	defer resp.Body.Close()

	maxBytes := w.maxFetchBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxFetchBytes
	}
	limited := io.LimitReader(resp.Body, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fetchResponse{}, err
	}
	truncated := int64(len(data)) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	contentType := resp.Header.Get("Content-Type")
	mediaType := ""
	if contentType != "" {
		mediaType, _, _ = mime.ParseMediaType(contentType)
		mediaType = strings.ToLower(mediaType)
	}
	out := fetchResponse{
		URL:            validated.String(),
		FinalURL:       resp.Request.URL.String(),
		StatusCode:     resp.StatusCode,
		Status:         resp.Status,
		ContentType:    contentType,
		MediaType:      mediaType,
		Headers:        safeHeaders(resp.Header),
		BytesRead:      len(data),
		MaxFetchBytes:  maxBytes,
		FetchTruncated: truncated,
	}
	mode := strings.ToLower(strings.TrimSpace(outputMode))
	if mode == "" {
		mode = "readability"
	}
	limit := outputLimit
	if limit <= 0 {
		limit = defaultOutputBytes
	}
	limit = clampInt(limit, 1, maxReadabilityBytes)
	text := decodeBodyText(data, mediaType)
	if mode == "raw" || mode == "text" {
		result := cappedTextResult(text, limit)
		out.Text = result["text"].(string)
		out.TextBytes = result["bytes"].(int)
		out.TextTruncated = result["truncated"].(bool)
		return out, nil
	}
	if mode == "none" || mode == "headers" {
		return out, nil
	}
	if strings.Contains(mediaType, "html") || looksLikeHTML(data) {
		readable := htmlToReadableText(bytes.NewReader(data))
		result := cappedTextResult(readable, limit)
		out.ReadabilityText = result["text"].(string)
		out.TextBytes = result["bytes"].(int)
		out.TextTruncated = result["truncated"].(bool)
	} else {
		result := cappedTextResult(text, limit)
		out.Text = result["text"].(string)
		out.TextBytes = result["bytes"].(int)
		out.TextTruncated = result["truncated"].(bool)
	}
	return out, nil
}

func (w *WebTools) fetchHTTPClient() *http.Client {
	base := &http.Client{Timeout: fetchTimeout, Transport: safeFetchTransport()}
	if w.httpClient != nil {
		copy := *w.httpClient
		base = &copy
		if base.Timeout <= 0 {
			base.Timeout = fetchTimeout
		}
		if base.Transport == nil {
			base.Transport = safeFetchTransport()
		}
	}
	base.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		if _, err := validateFetchURL(req.URL.String()); err != nil {
			return err
		}
		return nil
	}
	return base
}

func safeFetchTransport() http.RoundTripper {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = safeDialContext
	return transport
}

func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if isBlockedHost(host) {
		return nil, fmt.Errorf("url: resolved host is not public: %s", host)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("url: host did not resolve: %s", host)
	}
	for _, ip := range ips {
		if isBlockedIP(ip.IP) {
			return nil, fmt.Errorf("url: resolved host is not public: %s", host)
		}
	}
	return (&net.Dialer{Timeout: fetchTimeout}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

func validateFetchURL(rawURL string) (*url.URL, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, fmt.Errorf("url: required string")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("url: parse: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("url: only http and https URLs are allowed")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("url: embedded credentials are not allowed")
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return nil, fmt.Errorf("url: host is required")
	}
	if isBlockedHost(host) {
		return nil, fmt.Errorf("url: localhost, private, link-local, and otherwise non-public hosts are not allowed")
	}
	return parsed, nil
}

func isBlockedHost(host string) bool {
	h := strings.ToLower(strings.Trim(host, "[] ."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || h == "0.0.0.0" {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	return isBlockedIP(ip)
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

func safeHeaders(headers http.Header) map[string]any {
	allowed := []string{"Content-Type", "Content-Length", "Last-Modified", "ETag", "Location", "Cache-Control"}
	out := map[string]any{}
	for _, key := range allowed {
		if value := headers.Values(key); len(value) > 0 {
			out[key] = strings.Join(value, ", ")
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func decodeBodyText(data []byte, mediaType string) string {
	return strings.TrimSpace(string(data))
}

func looksLikeHTML(data []byte) bool {
	prefix := strings.ToLower(string(data[:min(len(data), 512)]))
	return strings.Contains(prefix, "<html") || strings.Contains(prefix, "<!doctype html") || strings.Contains(prefix, "<body")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
