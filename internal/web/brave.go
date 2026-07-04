package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

var braveSearchEndpoint = "https://api.search.brave.com/res/v1/web/search"

type braveProvider struct {
	apiKey string
	client *http.Client
}

func newBraveProvider(apiKey string, client *http.Client) SearchProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &braveProvider{apiKey: strings.TrimSpace(apiKey), client: client}
}

func (p *braveProvider) Name() string        { return "brave" }
func (p *braveProvider) DisplayName() string { return "Brave Search" }
func (p *braveProvider) Configured() bool    { return p.apiKey != "" }
func (p *braveProvider) Supported() bool     { return true }
func (p *braveProvider) Status() ProviderStatus {
	reason := "Brave Search API key is configured"
	if !p.Configured() {
		reason = "BRAVE_API_KEY or --brave-api-key is not configured"
	}
	return ProviderStatus{Name: p.Name(), DisplayName: p.DisplayName(), Configured: p.Configured(), Supported: true, Reason: reason}
}

func (p *braveProvider) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	if !p.Configured() {
		return nil, fmt.Errorf("Brave Search API key is not configured")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query: required string")
	}
	limit := clampInt(opts.Limit, 1, 20)
	reqURL, err := url.Parse(braveSearchEndpoint)
	if err != nil {
		return nil, err
	}
	values := reqURL.Query()
	values.Set("q", query)
	values.Set("count", fmt.Sprintf("%d", limit))
	values.Set("text_decorations", "false")
	values.Set("search_lang", "en")
	reqURL.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	// Do not set Accept-Encoding manually; Go's default transport will request
	// and transparently decompress gzip responses when it owns that header.
	req.Header.Set("User-Agent", "nullbot-web-mcp/0.1")
	req.Header.Set("X-Subscription-Token", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Brave Search API returned %s", resp.Status)
	}
	var payload braveSearchResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Brave Search response: %w", err)
	}
	return normalizeBraveResults(payload, limit), nil
}

type braveSearchResponse struct {
	Query struct {
		Original string `json:"original"`
	} `json:"query"`
	Web struct {
		Results []braveWebResult `json:"results"`
	} `json:"web"`
	News struct {
		Results []braveWebResult `json:"results"`
	} `json:"news"`
}

type braveWebResult struct {
	Title       string             `json:"title"`
	URL         string             `json:"url"`
	Description string             `json:"description"`
	Age         string             `json:"age"`
	Language    string             `json:"language"`
	Family      braveFlexibleValue `json:"family_friendly"`
	SubType     string             `json:"subtype"`
	Profile     *struct {
		Name     string `json:"name"`
		LongName string `json:"long_name"`
	} `json:"profile"`
	ExtraSnippets []string `json:"extra_snippets"`
}

type braveFlexibleValue struct {
	Set   bool
	Value any
}

func (v *braveFlexibleValue) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		return nil
	}
	switch raw {
	case "true":
		v.Set = true
		v.Value = true
		return nil
	case "false":
		v.Set = true
		v.Value = false
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	switch strings.ToLower(text) {
	case "true", "yes":
		v.Set = true
		v.Value = true
	case "false", "no":
		v.Set = true
		v.Value = false
	default:
		v.Set = true
		v.Value = text
	}
	return nil
}

func normalizeBraveResults(payload braveSearchResponse, limit int) []SearchResult {
	limit = clampInt(limit, 1, 20)
	results := make([]SearchResult, 0, limit)
	for _, item := range payload.Web.Results {
		if len(results) >= limit {
			break
		}
		urlValue := strings.TrimSpace(item.URL)
		title := strings.TrimSpace(stripTags(item.Title))
		if urlValue == "" || title == "" {
			continue
		}
		metadata := map[string]any{}
		if item.Age != "" {
			metadata["age"] = item.Age
		}
		if item.Language != "" {
			metadata["language"] = item.Language
		}
		if item.Family.Set {
			metadata["family_friendly"] = item.Family.Value
		}
		if item.Profile != nil && (item.Profile.Name != "" || item.Profile.LongName != "") {
			metadata["profile"] = map[string]any{"name": item.Profile.Name, "long_name": item.Profile.LongName}
		}
		if len(item.ExtraSnippets) > 0 {
			metadata["extra_snippets"] = item.ExtraSnippets
		}
		if len(metadata) == 0 {
			metadata = nil
		}
		results = append(results, SearchResult{
			Title:    title,
			URL:      urlValue,
			Snippet:  strings.TrimSpace(stripTags(item.Description)),
			Rank:     len(results) + 1,
			Provider: "brave",
			Metadata: metadata,
		})
	}
	return results
}
