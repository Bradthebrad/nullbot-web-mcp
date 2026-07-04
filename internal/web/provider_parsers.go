package web

import "strings"

type tavilySearchResponse struct {
	Results []tavilySearchResult `json:"results"`
}

type tavilySearchResult struct {
	Title      string  `json:"title"`
	URL        string  `json:"url"`
	Content    string  `json:"content"`
	RawContent string  `json:"raw_content"`
	Score      float64 `json:"score"`
}

func normalizeTavilyResults(payload tavilySearchResponse, limit int) []SearchResult {
	limit = clampInt(limit, 1, 20)
	results := make([]SearchResult, 0, limit)
	for _, item := range payload.Results {
		if len(results) >= limit {
			break
		}
		urlValue := strings.TrimSpace(item.URL)
		title := strings.TrimSpace(stripTags(item.Title))
		if urlValue == "" || title == "" {
			continue
		}
		snippet := strings.TrimSpace(stripTags(item.Content))
		if snippet == "" {
			snippet = strings.TrimSpace(stripTags(item.RawContent))
		}
		metadata := map[string]any{}
		if item.Score != 0 {
			metadata["score"] = item.Score
		}
		if item.RawContent != "" {
			metadata["has_raw_content"] = true
		}
		if len(metadata) == 0 {
			metadata = nil
		}
		results = append(results, SearchResult{Title: title, URL: urlValue, Snippet: snippet, Rank: len(results) + 1, Provider: "tavily", Metadata: metadata})
	}
	return results
}

type serpAPISearchResponse struct {
	OrganicResults []serpAPIOrganicResult `json:"organic_results"`
}

type serpAPIOrganicResult struct {
	Position      int      `json:"position"`
	Title         string   `json:"title"`
	Link          string   `json:"link"`
	Snippet       string   `json:"snippet"`
	DisplayedLink string   `json:"displayed_link"`
	Source        string   `json:"source"`
	Date          string   `json:"date"`
	Sitelinks     any      `json:"sitelinks"`
	RichSnippet   any      `json:"rich_snippet"`
	CachedPageURL string   `json:"cached_page_link"`
	RelatedPages  []string `json:"related_pages_link"`
}

func normalizeSerpAPIResults(payload serpAPISearchResponse, limit int) []SearchResult {
	limit = clampInt(limit, 1, 20)
	results := make([]SearchResult, 0, limit)
	for _, item := range payload.OrganicResults {
		if len(results) >= limit {
			break
		}
		urlValue := strings.TrimSpace(item.Link)
		title := strings.TrimSpace(stripTags(item.Title))
		if urlValue == "" || title == "" {
			continue
		}
		metadata := map[string]any{}
		if item.Position != 0 {
			metadata["position"] = item.Position
		}
		if item.DisplayedLink != "" {
			metadata["displayed_link"] = item.DisplayedLink
		}
		if item.Source != "" {
			metadata["source"] = item.Source
		}
		if item.Date != "" {
			metadata["date"] = item.Date
		}
		if item.CachedPageURL != "" {
			metadata["cached_page_link"] = item.CachedPageURL
		}
		if len(metadata) == 0 {
			metadata = nil
		}
		results = append(results, SearchResult{Title: title, URL: urlValue, Snippet: strings.TrimSpace(stripTags(item.Snippet)), Rank: len(results) + 1, Provider: "serpapi", Metadata: metadata})
	}
	return results
}

type bingSearchResponse struct {
	WebPages struct {
		Value []bingWebPageResult `json:"value"`
	} `json:"webPages"`
}

type bingWebPageResult struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	URL              string   `json:"url"`
	DisplayURL       string   `json:"displayUrl"`
	Snippet          string   `json:"snippet"`
	DateLastCrawled  string   `json:"dateLastCrawled"`
	Language         string   `json:"language"`
	IsFamilyFriendly bool     `json:"isFamilyFriendly"`
	DeepLinks        []string `json:"deepLinks"`
}

func normalizeBingResults(payload bingSearchResponse, limit int) []SearchResult {
	limit = clampInt(limit, 1, 20)
	results := make([]SearchResult, 0, limit)
	for _, item := range payload.WebPages.Value {
		if len(results) >= limit {
			break
		}
		urlValue := strings.TrimSpace(item.URL)
		title := strings.TrimSpace(stripTags(item.Name))
		if urlValue == "" || title == "" {
			continue
		}
		metadata := map[string]any{}
		if item.DisplayURL != "" {
			metadata["display_url"] = item.DisplayURL
		}
		if item.DateLastCrawled != "" {
			metadata["date_last_crawled"] = item.DateLastCrawled
		}
		if item.Language != "" {
			metadata["language"] = item.Language
		}
		metadata["is_family_friendly"] = item.IsFamilyFriendly
		if len(metadata) == 0 {
			metadata = nil
		}
		results = append(results, SearchResult{Title: title, URL: urlValue, Snippet: strings.TrimSpace(stripTags(item.Snippet)), Rank: len(results) + 1, Provider: "bing", Metadata: metadata})
	}
	return results
}
