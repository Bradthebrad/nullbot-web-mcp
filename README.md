# nullbot-web-mcp

Pure-Go NullBot MCP server for web search, safe URL fetching/readability, and native Chromium-family browser automation through localhost Chrome DevTools Protocol (CDP) attach.

The server follows the NullBot MCP family layout and supports `stdio` by default plus HTTP/SSE transports for local development.

## Features

- Search the web through a provider abstraction.
  - Brave Search is implemented in this build.
  - Tavily, SerpAPI, and Bing are recognized and report clear not-configured/not-implemented status.
- Fetch public `http`/`https` URLs with SSRF-oriented safety checks, byte caps, redirect limits, and HTML readability extraction.
- Attach to, launch, inspect, and automate local Chromium-family browsers through CDP.
- Capture screenshots only to workspace-relative PNG paths protected by workspace escape checks.
- Gate JavaScript evaluation behind `--allow-eval=false` by default.

## Tools

| Tool | Permission area | Description |
| --- | --- | --- |
| `web_workspace_info` | metadata | Reports workspace policy, search provider/key presence, browser/CDP configuration, eval gate, fetch cap, and exposed tool list. |
| `list_search_providers` | metadata | Lists known providers, configured provider, selected provider, and boolean key presence without exposing key values. |
| `web_search` | `network_fetch` | Searches with the selected provider. Brave is implemented; other providers are listed as stubs in this build. |
| `fetch_url` | `network_fetch` | Fetches safe public `http`/`https` URLs, rejects local/private targets, caps output, and can return readability text, raw/text body, or headers. |
| `browser_status` | `browser_control` metadata | Probes the configured localhost CDP endpoint and reports reachability, attach state, tabs, active target, and actionable instructions. |
| `browser_attach` | `browser_control` | Attaches to an already-running localhost Chromium CDP endpoint. |
| `browser_launch` | `browser_control` | Opt-in launcher for Chrome/Edge/Brave/Chromium with `--remote-debugging-address=127.0.0.1`; uses an isolated temporary profile by default. |
| `browser_navigate` | `browser_control`, `network_fetch` | Navigates the active attached tab to an `http`/`https` URL and waits for page readiness. |
| `browser_read` | `browser_control` | Reads text, readability output, DOM outline, or HTML from the active tab or a CSS selector with output caps. |
| `browser_screenshot` | `browser_control`, `workspace_write` | Captures viewport or full-page PNG to a workspace-relative `output_path`. |
| `browser_query` | `browser_control` | Queries CSS selectors and returns capped matches with text, attributes, visibility hints, and bounding boxes. |
| `browser_click` | `browser_control` | Clicks by CSS selector or coordinates. Confirm before consequential actions. |
| `browser_type` | `browser_control` | Types text into a selected/focused element, optionally clearing or submitting. Confirm before private-data or consequential form submission. |
| `browser_tabs` | `browser_control` | Lists, selects, opens, and closes CDP targets/tabs; protects against closing the last page unless forced. |
| `browser_eval` | `browser_control` | Evaluates bounded JavaScript only when `--allow-eval=true`; disabled by default. |

## Build and test

```powershell
go test ./...
go vet ./...
go build -o .\bin\nullbot-web-mcp.exe .\cmd\nullbot-web-mcp
```

## Run

### NullBot/default stdio transport

```powershell
.\bin\nullbot-web-mcp.exe --transport stdio --workspace C:\path\to\workspace
```

### Local HTTP transport for debugging

```powershell
.\bin\nullbot-web-mcp.exe --transport streamable-http --addr 127.0.0.1:8780 --path /mcp --workspace .
```

Common flags:

| Flag | Default | Notes |
| --- | --- | --- |
| `--transport` | `stdio` | `stdio`, `streamable-http`, `http`, or `sse`. |
| `--addr` | `127.0.0.1:8780` | HTTP/SSE listen address. Keep loopback for local use. |
| `--path` | `/mcp` | Streamable HTTP endpoint path. |
| `--sse-path` | `/sse` | Legacy SSE endpoint path. |
| `--message-path` | `/message` | Legacy SSE message endpoint path. |
| `--workspace` | `.` | Root for workspace-relative output paths. |
| `--search-provider` | `auto` | `auto`, `brave`, `tavily`, `serpapi`, `bing`, or `none`. |
| `--cdp-endpoint` | `http://127.0.0.1:9222` | Localhost CDP endpoint for browser attach/status. |
| `--browser-path` | empty | Optional Chrome/Edge/Brave executable for `browser_launch`. |
| `--allow-eval` | `false` | Enables `browser_eval` only when explicitly true. |
| `--brave-api-key` | empty | Falls back to `BRAVE_API_KEY`. |
| `--tavily-api-key` | empty | Falls back to `TAVILY_API_KEY`; provider is not implemented yet. |
| `--serpapi-api-key` | empty | Falls back to `SERPAPI_API_KEY`; provider is not implemented yet. |
| `--bing-api-key` | empty | Falls back to `BING_API_KEY`; provider is not implemented yet. |
| `--max-fetch-bytes` | `524288` | Server fetch cap. Tool output also has per-call caps. |
| `--version` | false | Print version and exit. |

## Search setup

Brave Search is the first working provider:

```powershell
$env:BRAVE_API_KEY = "..."
.\bin\nullbot-web-mcp.exe --transport stdio --search-provider auto
```

With `--search-provider auto`, Brave is selected when `BRAVE_API_KEY` or `--brave-api-key` is present. API key values are never returned by `web_workspace_info` or `list_search_providers`; only boolean key-presence status is reported.

Example tool flow:

1. Call `list_search_providers` to confirm provider status.
2. Call `web_search` with a focused query and modest limit.
3. Use `fetch_url` on promising result URLs to read the source page directly.
4. Prefer citing fetched pages over search snippets.

## URL fetching and readability

`fetch_url` is intended for public web pages. It:

- allows only `http` and `https`,
- rejects embedded credentials,
- rejects localhost, private, link-local, multicast, unspecified, and otherwise non-public IP targets where feasible,
- disables proxy use for default fetches,
- revalidates redirects and enforces a redirect limit,
- caps response reads,
- returns a safe response-header subset,
- can return `readability`, `text`, `raw`, `headers`, or `none` output modes.

Example arguments:

```json
{
  "url": "https://example.com/",
  "output_mode": "readability",
  "output_limit": 12000
}
```

## Browser control model

`nullbot-web-mcp` uses CDP attach for v0.1. It only accepts localhost CDP endpoints such as:

- `http://127.0.0.1:9222`
- `http://localhost:9222`
- `ws://127.0.0.1:9222/devtools/browser/<id>`

Remote/non-loopback CDP endpoints are rejected.

### Option A: attach to a manually launched browser

Chrome example:

```powershell
& "C:\Program Files\Google\Chrome\Application\chrome.exe" `
  --remote-debugging-address=127.0.0.1 `
  --remote-debugging-port=9222 `
  --user-data-dir="$env:TEMP\nullbot-chrome-profile"
```

Edge and Brave use the same CDP flags with their executable path.

Then call:

1. `browser_status`
2. `browser_attach`
3. `browser_navigate`
4. `browser_read`, `browser_query`, `browser_screenshot`, etc.

### Option B: use browser_launch

`browser_launch` starts a Chromium-family browser with remote debugging bound to `127.0.0.1` and an isolated temporary profile by default. You can pass `browser_path` or configure `--browser-path` if auto-detection does not find your browser.

Profile strategy:

- Omit `profile_dir` for the safest default: an isolated temporary profile.
- Set `temporary_profile=true` to force a temporary profile.
- Provide `profile_dir` only when you intentionally want a persistent user-data-dir.

Do not casually point `profile_dir` at your normal everyday browser profile. Real profiles may include cookies, sessions, history, downloads, extensions, and logged-in accounts.

## Safety and permissions

See [SECURITY.md](SECURITY.md) for the full safety notes. Key points:

- `network_fetch`: `web_search`, `fetch_url`, and browser navigation may contact external sites.
- `browser_control`: browser tools can operate a real browser and may interact with logged-in sessions if attached to a real profile.
- `workspace_write`: `browser_screenshot` writes PNG files under the configured workspace only.
- CDP endpoints are restricted to localhost loopback addresses.
- `browser_eval` is disabled unless the server starts with `--allow-eval=true`.
- Confirm with the user before purchases, sends, deletes, account changes, private-data submission, or other consequential browser actions.

## Examples

### Check configuration

Call `web_workspace_info` first when diagnosing setup. It reports the workspace, provider/key presence, CDP endpoint, eval gate, fetch caps, and tool list.

### Search then fetch

```json
{
  "query": "NullBot MCP server examples",
  "limit": 5,
  "provider": "auto"
}
```

Then fetch a selected public result URL:

```json
{
  "url": "https://example.com/article",
  "output_mode": "readability",
  "output_limit": 20000
}
```

### Browser read workflow

1. `browser_status`
2. `browser_launch` or manually launch Chrome and `browser_attach`
3. `browser_navigate` to a harmless public page
4. `browser_read` with `mode: "readability"`
5. `browser_screenshot` to `screenshots/page.png` if visual evidence is needed

## Troubleshooting

### `browser_status` says the endpoint is unreachable

Start a Chromium-family browser with:

```text
--remote-debugging-address=127.0.0.1 --remote-debugging-port=9222
```

Check that the port matches `--cdp-endpoint`.

### `browser_attach` reports no websocket URL

The endpoint may not be a real Chromium DevTools endpoint. Visit `http://127.0.0.1:9222/json/version` locally and confirm it returns `webSocketDebuggerUrl`.

### Search provider unavailable

Run `list_search_providers`. For Brave, set `BRAVE_API_KEY` or pass `--brave-api-key`. Tavily, SerpAPI, and Bing are status/reporting stubs in this build.

### Screenshot path rejected

Use a relative `.png` path under the configured workspace, for example `screenshots/home.png`. Absolute paths and `..` traversal are rejected.

### `browser_eval_disabled`

Restart the server with `--allow-eval=true` only if JavaScript evaluation is explicitly needed and approved.

## Limitations

- Brave Search is the only implemented live search provider in this build.
- Browser automation requires a local Chromium-family browser and CDP access.
- CDP attach cannot control non-Chromium browsers.
- `fetch_url` intentionally blocks localhost/private network targets, so it is not a general internal-network fetcher.
- Readability extraction is lightweight and may not perfectly capture every page layout.
- `browser_eval` is intentionally disabled by default.

## Release packaging

Local Windows release artifacts can be prepared with:

```powershell
.\scripts\release.ps1 -Version v0.1.0 -NoCompress
```

Without `-NoCompress`, the script also attempts to create a UPX-compressed `nullbot-web-mcp-small.exe`. The script writes artifacts under `dist\releases\<version>`, generates `SHA256SUMS.txt`, and writes local release notes. It does not publish a GitHub release.

## Development checks

```powershell
gofmt -w .
go vet ./...
go test ./...
```

See [docs/SMOKE_TESTS.md](docs/SMOKE_TESTS.md) for manual smoke-test steps before publishing or marketplace integration.
