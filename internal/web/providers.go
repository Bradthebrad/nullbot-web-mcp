package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const defaultSearchLimit = 10

type SearchResult struct {
	Title    string         `json:"title"`
	URL      string         `json:"url"`
	Snippet  string         `json:"snippet,omitempty"`
	Rank     int            `json:"rank"`
	Provider string         `json:"provider"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type SearchOptions struct {
	Limit int
}

type SearchProvider interface {
	Name() string
	DisplayName() string
	Configured() bool
	Supported() bool
	Status() ProviderStatus
	Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error)
}

type ProviderStatus struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Configured  bool   `json:"configured"`
	Selected    bool   `json:"selected"`
	Supported   bool   `json:"supported"`
	Reason      string `json:"reason"`
}

type unsupportedProvider struct {
	name        string
	displayName string
	apiKey      string
	reason      string
}

func (p unsupportedProvider) Name() string        { return p.name }
func (p unsupportedProvider) DisplayName() string { return p.displayName }
func (p unsupportedProvider) Configured() bool    { return strings.TrimSpace(p.apiKey) != "" }
func (p unsupportedProvider) Supported() bool     { return false }
func (p unsupportedProvider) Status() ProviderStatus {
	reason := p.reason
	if reason == "" {
		reason = "provider adapter is not implemented in this build"
	}
	if !p.Configured() {
		reason = "API key is not configured and provider adapter is not implemented in this build"
	}
	return ProviderStatus{Name: p.name, DisplayName: p.displayName, Configured: p.Configured(), Supported: false, Reason: reason}
}
func (p unsupportedProvider) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	if !p.Configured() {
		return nil, fmt.Errorf("search provider %q is not configured", p.name)
	}
	return nil, fmt.Errorf("search provider %q is not implemented in this build", p.name)
}

func (w *WebTools) searchProviders() []SearchProvider {
	client := w.httpClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return []SearchProvider{
		newBraveProvider(w.braveAPIKey, client),
		unsupportedProvider{name: "tavily", displayName: "Tavily Search", apiKey: w.tavilyAPIKey},
		unsupportedProvider{name: "serpapi", displayName: "SerpAPI", apiKey: w.serpAPIKey},
		unsupportedProvider{name: "bing", displayName: "Bing Web Search", apiKey: w.bingAPIKey},
	}
}

func (w *WebTools) providerByName(name string) SearchProvider {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, provider := range w.searchProviders() {
		if provider.Name() == name {
			return provider
		}
	}
	return nil
}

func (w *WebTools) providerStatuses() []ProviderStatus {
	selected := w.selectedProvider()
	statuses := make([]ProviderStatus, 0, len(w.searchProviders()))
	for _, provider := range w.searchProviders() {
		status := provider.Status()
		status.Selected = provider.Name() == selected
		statuses = append(statuses, status)
	}
	return statuses
}

func (w *WebTools) selectedProvider() string {
	provider := strings.ToLower(strings.TrimSpace(w.searchProvider))
	if provider == "" || provider == "auto" {
		for _, candidate := range w.searchProviders() {
			if candidate.Configured() {
				return candidate.Name()
			}
		}
		return "none"
	}
	if provider == "none" {
		return "none"
	}
	return provider
}

func (w *WebTools) selectedSearchProvider() (SearchProvider, error) {
	selected := w.selectedProvider()
	if selected == "none" {
		return nil, fmt.Errorf("no search provider selected or configured")
	}
	provider := w.providerByName(selected)
	if provider == nil {
		return nil, fmt.Errorf("unknown search provider %q", selected)
	}
	if !provider.Configured() {
		return nil, fmt.Errorf("search provider %q is not configured; provide an API key or choose another provider", selected)
	}
	if !provider.Supported() {
		return nil, fmt.Errorf("search provider %q is not implemented in this build", selected)
	}
	return provider, nil
}

func (w *WebTools) providerKeyPresent(provider string) bool {
	p := w.providerByName(provider)
	return p != nil && p.Configured()
}

func (w *WebTools) keyStatus() map[string]any {
	return map[string]any{
		"brave":   w.braveAPIKey != "",
		"tavily":  w.tavilyAPIKey != "",
		"serpapi": w.serpAPIKey != "",
		"bing":    w.bingAPIKey != "",
	}
}

func (w *WebTools) providerStatus() map[string]any {
	selected := w.selectedProvider()
	if selected == "none" {
		return map[string]any{"available": false, "reason": "no search provider selected or configured"}
	}
	provider := w.providerByName(selected)
	if provider == nil {
		return map[string]any{"available": false, "reason": "unknown selected provider"}
	}
	status := provider.Status()
	return map[string]any{"available": status.Configured && status.Supported, "reason": status.Reason}
}
