package web

import (
	"encoding/json"
	"testing"
)

func TestProviderParserSamplesNormalizeWithoutLiveAPICalls(t *testing.T) {
	t.Run("brave", func(t *testing.T) {
		var payload braveSearchResponse
		mustUnmarshalProviderSample(t, `{
			"web":{"results":[
				{"title":"<b>Brave</b> Result","url":"https://example.com/brave","description":"Brave <em>snippet</em>","age":"1 day ago","language":"en","profile":{"name":"Example","long_name":"Example Site"},"extra_snippets":["extra"]},
				{"title":"missing url","description":"skip"},
				{"title":"Second Brave","url":"https://example.com/brave-2","description":"Second"}
			]}
		}`, &payload)
		results := normalizeBraveResults(payload, 10)
		assertProviderResult(t, results, 2, "brave", "Brave Result", "https://example.com/brave", "Brave snippet")
		if results[0].Metadata["age"] != "1 day ago" || results[0].Metadata["language"] != "en" {
			t.Fatalf("expected Brave metadata, got %#v", results[0].Metadata)
		}
		if results[1].Rank != 2 || results[1].Title != "Second Brave" {
			t.Fatalf("expected malformed Brave item skipped/reranked, got %#v", results[1])
		}
	})

	t.Run("tavily", func(t *testing.T) {
		var payload tavilySearchResponse
		mustUnmarshalProviderSample(t, `{
			"results":[
				{"title":"<b>Tavily</b> Result","url":"https://example.com/tavily","content":"Tavily <em>snippet</em>","raw_content":"Raw content","score":0.91},
				{"title":"missing url","content":"skip"},
				{"title":"Second Tavily","url":"https://example.com/tavily-2","raw_content":"Raw fallback"}
			]
		}`, &payload)
		results := normalizeTavilyResults(payload, 10)
		assertProviderResult(t, results, 2, "tavily", "Tavily Result", "https://example.com/tavily", "Tavily snippet")
		if results[0].Metadata["score"] != 0.91 || results[0].Metadata["has_raw_content"] != true {
			t.Fatalf("expected Tavily metadata, got %#v", results[0].Metadata)
		}
		if results[1].Snippet != "Raw fallback" || results[1].Rank != 2 {
			t.Fatalf("expected Tavily raw_content fallback and rerank, got %#v", results[1])
		}
	})

	t.Run("serpapi", func(t *testing.T) {
		var payload serpAPISearchResponse
		mustUnmarshalProviderSample(t, `{
			"organic_results":[
				{"position":3,"title":"<b>SerpAPI</b> Result","link":"https://example.com/serpapi","snippet":"SerpAPI <em>snippet</em>","displayed_link":"example.com › serpapi","source":"Example","date":"Jun 2026","cached_page_link":"https://webcache.example/serpapi"},
				{"title":"missing link","snippet":"skip"},
				{"position":9,"title":"Second SerpAPI","link":"https://example.com/serpapi-2","snippet":"Second"}
			]
		}`, &payload)
		results := normalizeSerpAPIResults(payload, 10)
		assertProviderResult(t, results, 2, "serpapi", "SerpAPI Result", "https://example.com/serpapi", "SerpAPI snippet")
		if results[0].Metadata["position"] != float64(3) && results[0].Metadata["position"] != 3 {
			t.Fatalf("expected SerpAPI original position metadata, got %#v", results[0].Metadata)
		}
		if results[0].Metadata["displayed_link"] != "example.com › serpapi" || results[0].Metadata["source"] != "Example" {
			t.Fatalf("expected SerpAPI metadata, got %#v", results[0].Metadata)
		}
		if results[1].Rank != 2 || results[1].Title != "Second SerpAPI" {
			t.Fatalf("expected malformed SerpAPI item skipped/reranked, got %#v", results[1])
		}
	})

	t.Run("bing", func(t *testing.T) {
		var payload bingSearchResponse
		mustUnmarshalProviderSample(t, `{
			"webPages":{"value":[
				{"name":"<b>Bing</b> Result","url":"https://example.com/bing","displayUrl":"example.com/bing","snippet":"Bing <em>snippet</em>","dateLastCrawled":"2026-06-01T00:00:00Z","language":"en","isFamilyFriendly":true},
				{"name":"missing url","snippet":"skip"},
				{"name":"Second Bing","url":"https://example.com/bing-2","snippet":"Second"}
			]}
		}`, &payload)
		results := normalizeBingResults(payload, 10)
		assertProviderResult(t, results, 2, "bing", "Bing Result", "https://example.com/bing", "Bing snippet")
		if results[0].Metadata["display_url"] != "example.com/bing" || results[0].Metadata["language"] != "en" || results[0].Metadata["is_family_friendly"] != true {
			t.Fatalf("expected Bing metadata, got %#v", results[0].Metadata)
		}
		if results[1].Rank != 2 || results[1].Title != "Second Bing" {
			t.Fatalf("expected malformed Bing item skipped/reranked, got %#v", results[1])
		}
	})
}

func mustUnmarshalProviderSample(t *testing.T, sample string, dest any) {
	t.Helper()
	if err := json.Unmarshal([]byte(sample), dest); err != nil {
		t.Fatalf("unmarshal provider sample: %v", err)
	}
}

func assertProviderResult(t *testing.T, results []SearchResult, wantLen int, provider, title, urlValue, snippet string) {
	t.Helper()
	if len(results) != wantLen {
		t.Fatalf("expected %d results, got %d: %#v", wantLen, len(results), results)
	}
	first := results[0]
	if first.Provider != provider || first.Title != title || first.URL != urlValue || first.Snippet != snippet || first.Rank != 1 {
		t.Fatalf("unexpected normalized result: %#v", first)
	}
	if first.Metadata == nil {
		t.Fatalf("expected metadata on first %s result", provider)
	}
}
