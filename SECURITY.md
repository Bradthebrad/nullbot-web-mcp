# Security and permissions

`nullbot-web-mcp` combines network access, browser control, and workspace writes. Treat it as a powerful local automation server and keep the default loopback-only settings unless you have a specific reason to change them.

## Permission summary

| Permission area | Tools | Risk | Main mitigations |
| --- | --- | --- | --- |
| `network_fetch` | `web_search`, `fetch_url`, `browser_navigate` | Contacts external sites, downloads content, may reveal queries/visited URLs to providers/sites. | Use public URLs only, avoid secrets in queries/URLs, byte caps, redirect checks, SSRF-oriented blocking. |
| `browser_control` | `browser_status`, `browser_attach`, `browser_launch`, `browser_navigate`, `browser_read`, `browser_query`, `browser_click`, `browser_type`, `browser_tabs`, `browser_eval`, `browser_screenshot` | Can inspect or operate a real browser session. Mutating actions can click, type, submit, close tabs, or run JavaScript. | Localhost-only CDP, confirmation guidance, isolated temporary profiles by default for launch, eval disabled by default. |
| `workspace_write` | `browser_screenshot` | Writes files to disk. | Requires workspace-relative `.png` path, rejects absolute paths and traversal, resolves parent symlinks, writes only under configured workspace. |

## Network fetch behavior

`fetch_url` is designed for public web content. It rejects:

- non-HTTP(S) schemes,
- malformed or empty URLs,
- URLs with embedded credentials,
- localhost and loopback targets,
- private, link-local, multicast, unspecified, and otherwise non-public IP targets where feasible,
- redirects to unsafe targets.

The default fetch transport disables proxy use. This prevents local proxy environment variables from silently bypassing the target checks for normal operation.

`web_search` sends user queries to the selected search provider. Brave Search is implemented. Tavily, SerpAPI, and Bing are recognized but not implemented as live search adapters in this build.

## Browser control risks

Browser tools can observe and manipulate real web pages. If attached to a browser profile that is logged in to services, a tool may be able to interact with those sessions.

Always confirm with the user before using browser tools for consequential actions, including:

- purchases,
- sends or submissions,
- deletes,
- account or permission changes,
- private-data entry or submission,
- financial, medical, legal, employment, or identity-related workflows,
- other actions that could have external side effects.

Read-only browser tools (`browser_status`, `browser_read`, `browser_query`, screenshots) can still expose sensitive page content if the browser is logged in. Treat captured text and screenshots as potentially sensitive.

## Localhost-only CDP endpoints

CDP is powerful. Exposing it on a network interface can allow other processes or users to control the browser. This server only accepts loopback CDP endpoints such as:

- `http://127.0.0.1:9222`
- `http://localhost:9222`
- `ws://127.0.0.1:9222/devtools/browser/<id>`

Start browsers with:

```text
--remote-debugging-address=127.0.0.1 --remote-debugging-port=9222
```

Do not bind Chrome DevTools Protocol to `0.0.0.0`, a LAN IP, or a public interface.

## Browser profile strategy

`browser_launch` uses an isolated temporary profile by default. This is the safest mode because it avoids normal cookies, sessions, history, extensions, downloads, and account state.

Persistent `profile_dir` usage is opt-in. Only use a persistent profile when you intentionally want the browser to retain state across runs. Avoid pointing `profile_dir` at an everyday personal browser profile.

If you manually launch a browser, prefer a separate user-data-dir:

```powershell
& "C:\Program Files\Google\Chrome\Application\chrome.exe" `
  --remote-debugging-address=127.0.0.1 `
  --remote-debugging-port=9222 `
  --user-data-dir="$env:TEMP\nullbot-chrome-profile"
```

## JavaScript evaluation gate

`browser_eval` is disabled by default and returns `browser_eval_disabled` unless the server is started with:

```powershell
--allow-eval=true
```

Only enable it when explicitly needed. JavaScript evaluation can read or modify page state, access page-visible data, trigger network requests, submit forms, or perform other side effects depending on the expression and page context.

Even with `--allow-eval=true`, keep expressions bounded and purpose-specific. Do not use `browser_eval` for consequential actions without explicit confirmation.

## Workspace write safety

`browser_screenshot` writes PNG files under the configured workspace only. The server rejects:

- empty output paths,
- absolute paths,
- parent traversal such as `..`,
- backslash traversal variants,
- symlink parent escapes,
- non-`.png` extensions.

Use paths like:

```text
screenshots/example.png
```

Do not rely on screenshots as a secure evidence store; they may include sensitive page content.

## Secrets and API keys

API keys can be provided through flags or environment variables:

- `BRAVE_API_KEY` / `--brave-api-key`
- `TAVILY_API_KEY` / `--tavily-api-key`
- `SERPAPI_API_KEY` / `--serpapi-api-key`
- `BING_API_KEY` / `--bing-api-key`

The MCP tools report key presence as booleans only and do not return key values. Avoid placing real keys in shared scripts, logs, screenshots, or documentation.

## Publishing and marketplace gate

Release publishing, marketplace metadata edits, package installation, and MCP enabling are separate steps and require explicit approval. The local release script writes artifacts only; it does not publish by default.
