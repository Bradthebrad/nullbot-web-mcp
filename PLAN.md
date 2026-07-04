# Plan: `nullbot-web-mcp` — Web Search + Native Browser Automation MCP Server

Status: DRAFT FOR REVIEW. Nothing here is built yet. This document is the agreed scaffold/spec so we can execute together step by step.

This server follows the established NullBot family conventions discovered in the workspace:
- `github.com/Bradthebrad/tinychain/mcp` as the MCP runtime (`mcp.NewServer`, `server.AddTool`, `mcp.Tool`, `mcp.Text`, `mcp.ToolResult`).
- `cmd/<server>/main.go` + `internal/<domain>/...` layout.
- Standard transport flags (`stdio` default; `http`, `streamable-http`, `sse` optional).
- Workspace-relative path safety (mirrors `imagetools/workspace.go`).
- `scripts/release.ps1` producing normal `.exe`, UPX `-small.exe`, and `SHA256SUMS.txt`.
- Marketplace registration in `~/.nullbot/market/manifest.json` + `sources.json`.
- Skills published from the `nullbot-skills` repo as `skill_pack` packages.

---

## 1. Scope and the One Hard Problem

The user wants two capability groups:

1. **Web search** — query the web and return ranked results (titles, URLs, snippets), plus optional page fetch/readability extraction.
2. **Interactive browser actions in the user's *native* browser** — navigate, click, type, read DOM/text, screenshot, manage tabs, etc., in the browser the user actually uses (with their logins/cookies/profile), not a throwaway headless instance.

The hard problem is #2. A Go stdio child process cannot "just" puppeteer an already-running Chrome/Edge/Firefox window. We must choose a control mechanism. Options, with tradeoffs:

| Option | How it controls the native browser | Native profile/login? | Effort | Risk |
| --- | --- | --- | --- | --- |
| **A. Chrome DevTools Protocol (CDP) attach** | User launches their Chromium browser with `--remote-debugging-port`; server attaches over `ws://127.0.0.1:9222` | Yes (their real profile if launched with `--user-data-dir` pointing at it, or a dedicated automation profile) | Medium | User must start browser with a flag once; DevTools port is powerful/local-only |
| **B. Browser extension + native messaging / local WS bridge** | A small NullBot browser extension talks to the MCP server over a localhost WebSocket | Yes, fully native, no relaunch | High (must build + distribute an extension per browser) | Extension review/distribution friction |
| **C. WebDriver BiDi / Marionette (Firefox) / chromedriver** | Standard automation drivers | Usually a fresh profile; can point to real profile | Medium | Spawns/own-controls browser, less "native window" feel |
| **D. Playwright/Puppeteer `connectOverCDP`** | Same as A but via a heavy Node/Playwright dependency | Yes | Medium-High | Pulls a large non-Go runtime; against the "portable Go binary" ethos |

### Recommendation: **Option A (CDP attach) as the v0.1 core, with Option B (extension bridge) as the v0.2 "fully native, zero-flag" upgrade.**

Rationale:
- CDP attach keeps the server a **pure-Go, single binary** (uses `github.com/chromedp/chromedp` or raw CDP over `gorilla/websocket`), consistent with the family's portability ethos.
- It genuinely drives the user's Chromium browser (Chrome/Edge/Brave) with their session when they launch it with a debugging port (we provide a launcher helper tool + docs).
- The extension bridge (Option B) is the best long-term "native, no relaunch" answer but is a separate distribution problem; we scope it as a roadmap submodule so we are honest about effort.

> Decision needed from you before coding: confirm **Option A first**, or push straight to the extension bridge. The rest of this plan assumes A-first.

---

## 2. Repository Layout

```text
nullbot-web-mcp/
  go.mod                      # module github.com/Bradthebrad/nullbot-web-mcp
  go.sum
  .gitignore                  # copy from sibling repos (dist/, gocache, etc.)
  README.md
  PLAN.md                     # this file (can be dropped once shipped)
  cmd/
    nullbot-web-mcp/
      main.go                 # flags, key injection, server wiring (mirrors imagetools main.go)
  internal/
    web/
      workspace.go            # root resolve/ensureInside/rel (copied pattern)
      util.go                 # schema/pretty/textArg/intArg/boolArg/stringSliceArg helpers
      tools.go                # Tools() aggregator + web_workspace_info
      search.go               # search provider tools (web_search, fetch_url)
      browser.go              # CDP session manager + browser_* tools
      browser_launch.go       # native-browser launcher/attacher helpers
      providers.go            # search provider adapters (Brave/Tavily/SerpAPI/Bing)
      readability.go          # HTML -> clean text/markdown extraction
      web_test.go             # unit tests (schema, url validation, provider parsing)
  scripts/
    release.ps1               # adapted from imagetools release.ps1
```

`go.mod` uses tagged public Go modules:

```go
module github.com/Bradthebrad/nullbot-web-mcp

go 1.26.4

require github.com/Bradthebrad/tinychain v0.2.0
```

Likely third-party deps:
- `github.com/chromedp/chromedp` (CDP driver) **or** raw `github.com/gorilla/websocket` + `chromedp/cdproto` for a lighter footprint.
- `golang.org/x/net/html` for readability/DOM-to-text.
- Standard library `net/http` for search-provider calls and page fetches.

---

## 3. Tool Suite (v0.1)

### Group 0 — Introspection
| Tool | Purpose |
| --- | --- |
| `web_workspace_info` | Report workspace root, path policy, configured search provider + key status, browser attach status, and the tool list. Mirrors `image_workspace_info`. |

### Group 1 — Web Search & Fetch (no browser required)
| Tool | Purpose | Key/permission |
| --- | --- | --- |
| `web_search` | Run a search query against the configured provider; return ranked `{title, url, snippet}` results. Args: `query`, `count`, `freshness?`, `provider?`. | `network_fetch`, provider key |
| `fetch_url` | HTTP GET a URL; return status, headers subset, and readability-extracted text/markdown (capped). Args: `url`, `max_bytes?`, `format?` (text\|markdown\|raw). | `network_fetch` |
| `list_search_providers` | List supported providers and which have keys present. | none |

Search providers (adapter pattern in `providers.go`, env-key driven like imagetools):
- **Brave Search API** (`BRAVE_API_KEY`) — privacy-friendly default.
- **Tavily** (`TAVILY_API_KEY`) — agent-optimized result+answer.
- **SerpAPI** (`SERPAPI_API_KEY`) — Google-backed.
- **Bing Web Search** (`BING_API_KEY`) — fallback.
- Provider chosen by `--search-provider` flag / `NULLBOT_SEARCH_PROVIDER` env, else first provider with a present key.

### Group 2 — Native Browser Control (CDP attach)
| Tool | Purpose |
| --- | --- |
| `browser_status` | Report whether a CDP endpoint is reachable, active target/tab list, and current URL. |
| `browser_launch` | Launch the user's Chromium browser (Chrome/Edge/Brave) with `--remote-debugging-port` and an optional profile dir, then attach. Opt-in; documents the relaunch tradeoff. |
| `browser_attach` | Attach to an already-running browser at a given `ws`/`http` debug endpoint (e.g. `127.0.0.1:9222`). |
| `browser_navigate` | Navigate active tab to a URL; wait for load. |
| `browser_read` | Return readability text / DOM outline / selected-element text for the active tab. |
| `browser_query` | Query DOM by CSS selector; return matched element text/attributes (capped). |
| `browser_click` | Click an element by selector or coordinates. |
| `browser_type` | Type text into a focused/selected input; optional submit. |
| `browser_screenshot` | Capture viewport/full-page PNG into the workspace (returns `output_path`, like imagetools outputs). |
| `browser_tabs` | List/select/open/close tabs (targets). |
| `browser_eval` | Evaluate a bounded JS expression and return the JSON result. **Gated** behind `--allow-eval` because it is powerful. |

All browser tools fail with a clear message when no CDP endpoint is attached, instructing the user to run `browser_launch` or start their browser with a debug port.

---

## 4. Safety / Permission Model (must be explicit in README + manifest)

- **`network_fetch`** — search + URL fetch reach the public internet and third-party search APIs.
- **`browser_control`** — drives a real browser that may be logged into the user's accounts. This is high-trust. Tools that mutate page state (`click`, `type`, `navigate`, `eval`) are clearly destructive-capable; the client (NullBot) should confirm with the user before consequential actions (purchases, sends, deletes).
- **`workspace_write`** — only `browser_screenshot` and any saved-HTML tool write files, always workspace-relative (absolute paths rejected, no `..` escape — reuse the `ensureInside` pattern).
- **`browser_eval`** is **off by default**; requires `--allow-eval`.
- Debug-port endpoints are bound to `127.0.0.1` only; never expose remotely.
- Keys read from env (injected by NullBot from `~/.nullbot/api/keys.json`) or `--*-api-key` flags, exactly like imagetools.

---

## 5. main.go Flags (mirrors family conventions)

```
--transport stdio|streamable-http|http|sse   (default stdio)
--addr 127.0.0.1:8780                         (HTTP/SSE listen)
--path /mcp
--sse-path /sse
--message-path /message
--workspace .                                 (screenshot/output root)
--search-provider brave|tavily|serpapi|bing   (optional; auto-detect by key)
--cdp-endpoint 127.0.0.1:9222                  (default attach target)
--browser-path ""                             (override browser exe for launch)
--allow-eval=false                            (gate browser_eval)
--brave-api-key / --tavily-api-key / --serpapi-api-key / --bing-api-key
--max-fetch-bytes 5MB
--version
```

Port choice: family uses 8765 (code), 8770 (parsers), 8775 (imagetools) → **8780** for web; CDP default stays Chrome's conventional **9222**.

---

## 6. Tests (`web_test.go`)
- Schema builder produces valid `object` schemas with `required`.
- URL validation rejects non-http(s) and SSRF-y hosts where feasible.
- Provider response parsers map sample JSON → normalized results.
- Workspace `resolve`/`ensureInside` reject absolute + `..` escapes (copy imagetools test approach).
- Browser tools return the correct "not attached" error when no endpoint is set (no live browser needed in CI).

---

## 7. Build & Release (adapt `scripts/release.ps1`)
- `go test ./...`
- `go build -trimpath -ldflags "-s -w" -o dist/releases/<ver>/nullbot-web-mcp.exe ./cmd/nullbot-web-mcp`
- UPX `-small.exe` copy + `--best --lzma` (with AV/SmartScreen warning in notes).
- `SHA256SUMS.txt` via `Get-FileHash`.
- `RELEASE_NOTES.md` with tool summary + UPX warning.
- `gh release create v0.1.0 ...` — **only when you explicitly ask to publish**.

Smoke tests before release: `--version`, stdio `tools/list`, one `web_search`, one `browser_status` (no browser) for the friendly-error path.

---

## 8. Marketplace Integration

Add the repo to `~/.nullbot/market/sources.json` `repos` list, then add a package entry to `manifest.json` (run `market_refresh` after the GitHub release exists). Proposed package metadata:

```json
{
  "id": "nullbot-web-mcp",
  "kind": "mcp_server",
  "name": "NullBot Web MCP",
  "description": "Web search, URL fetch/readability, and interactive control of the user's native Chromium browser via CDP.",
  "repo": "Bradthebrad/nullbot-web-mcp",
  "release_tag": "v0.1.0",
  "assets": [
    { "name": "nullbot-web-mcp-small.exe", "platform": "windows", "arch": "amd64", "url": "https://github.com/Bradthebrad/nullbot-web-mcp/releases/download/v0.1.0/nullbot-web-mcp-small.exe", "sha256": "TBD", "compressed": true, "warning": "UPX-compressed binaries can trigger antivirus or SmartScreen heuristics on Windows." },
    { "name": "nullbot-web-mcp.exe", "platform": "windows", "arch": "amd64", "url": "https://github.com/Bradthebrad/nullbot-web-mcp/releases/download/v0.1.0/nullbot-web-mcp.exe", "sha256": "TBD" }
  ],
  "default_transport": "stdio",
  "default_args": ["--workspace", "{{workspace}}"],
  "permissions": ["network_fetch", "browser_control", "workspace_write"]
}
```

> New permission name `browser_control` is introduced; we should also list it in the mcp-skill permission vocabulary.

Install/enable flow once released: `market_refresh` → `market_install_package nullbot-web-mcp` → enable only when you explicitly ask.

---

## 9. Skills — published from `nullbot-skills` repo (`skill_pack`)

Follow the existing `skills/<name>/SKILL.md` layout with YAML front matter (`name`, `description`, `allowed-tools`). Proposed **one umbrella skill + several focused submodule skills**:

```text
nullbot-skills/skills/
  web-tools/            # umbrella: when to use web search vs browser, safety, workflow
    SKILL.md
  web-search/           # querying, provider selection, result triage, fetch_url + readability
    SKILL.md
  browser-automation/   # attach/launch model, navigate/read/click/type, screenshots, safety gates
    SKILL.md
  browser-research/     # task recipe: multi-step research/scrape using search + browser together
    SKILL.md
```

Each submodule SKILL.md will:
- Explain *when* it applies and *which* `nullbot-web-mcp` tools it orchestrates.
- State that it grants no powers itself; it requires `nullbot-web-mcp` installed + enabled.
- Encode safety rules (confirm before consequential browser actions, eval is gated, localhost-only debug port).
- Cross-link to the umbrella `web-tools` skill.

I will also register these as `skill_pack` packages in the marketplace manifest (mirroring `api-probe`/`mcp-skill`).

> Decision needed: do you want all four skills, or collapse to two (`web-search` + `browser-automation`)? I lean toward the four-skill split for progressive disclosure, matching how the SKILL system loads references on demand.

---

## 10. Proposed Execution Order (we do these together)

1. **You approve**: Option A-first, port 8780, the four-skill split, and `browser_control` permission name.
2. Scaffold repo: `go.mod`, `.gitignore`, `main.go`, `workspace.go`, `util.go`, `tools.go` (compiles, `web_workspace_info` only).
3. Implement Group 1 (`web_search`, `fetch_url`, `list_search_providers`) + one provider (Brave) + readability.
4. Implement Group 2 CDP core (`browser_status`, `browser_attach`, `browser_launch`, `browser_navigate`, `browser_read`, `browser_screenshot`).
5. Add remaining browser tools (`click`, `type`, `query`, `tabs`, gated `eval`).
6. Tests + `go test ./...` green.
7. README + `scripts/release.ps1`.
8. Build + smoke test (no publish yet).
9. Write the four SKILL.md files (locally via `create_skill`, and in the `nullbot-skills` repo).
10. On your explicit go: publish GitHub release, update `sources.json` + `manifest.json`, `market_refresh`, install, and enable.

---

## 11. Open Questions for You

1. **Browser control mechanism**: confirm CDP-attach first (Option A) vs. invest in the extension bridge (Option B) now.
2. **Default search provider**: Brave default OK, or do you already have a Tavily/SerpAPI key you'd rather lead with?
3. **Skill granularity**: four skills (umbrella + 3) or two?
4. **`browser_eval`**: keep it in v0.1 (gated off) or defer entirely?
5. **Profile strategy for `browser_launch`**: attach to the *real* user profile (full logins, higher risk) or a dedicated `nullbot-automation` profile by default?
```
