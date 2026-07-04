# Smoke tests

Use this checklist after building local artifacts and before any publish or marketplace integration step.

These checks are intended to be safe and local. Do not create GitHub releases, edit marketplace metadata, install packages, or enable MCP servers as part of this checklist unless a separate plan step explicitly approves it.

## 1. Build/test baseline

From the repository root:

```powershell
gofmt -w .
go vet ./...
go test ./...
go build -trimpath -ldflags "-s -w" -o .\bin\nullbot-web-mcp.exe .\cmd\nullbot-web-mcp
```

Expected:

- `go vet ./...` exits `0`.
- `go test ./...` exits `0`.
- `bin\nullbot-web-mcp.exe` exists.

## 2. Version smoke test

```powershell
.\bin\nullbot-web-mcp.exe --version
```

Expected output:

```text
nullbot-web-mcp 0.1.0
```

## 3. Stdio tools/list smoke test

Start the server over stdio and send an MCP `tools/list` request. One manual PowerShell option is:

```powershell
$body = '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
$body | .\bin\nullbot-web-mcp.exe --transport stdio --workspace .
```

Expected:

- JSON-RPC response contains a `tools` array.
- Tool names include:
  - `web_workspace_info`
  - `list_search_providers`
  - `web_search`
  - `fetch_url`
  - `browser_status`
  - `browser_attach`
  - `browser_launch`
  - `browser_navigate`
  - `browser_read`
  - `browser_screenshot`
  - `browser_query`
  - `browser_click`
  - `browser_type`
  - `browser_tabs`
  - `browser_eval`

If the local MCP stdio framing expects a different harness, use the NullBot client or sibling MCP test harness and verify the same `tools/list` result.

## 4. web_workspace_info smoke test

Call `web_workspace_info` through MCP.

Expected payload includes:

- `ok: true`,
- workspace root and path policy,
- selected/configured search provider,
- key-presence booleans only, not key values,
- CDP endpoint defaults/status,
- `allow_eval` state,
- `max_fetch_bytes`,
- current tool list.

## 5. Search provider smoke test

If a Brave API key is available:

```powershell
$env:BRAVE_API_KEY = "<key>"
```

Start the server and call `list_search_providers`.

Expected:

- Brave reports `configured: true` and `supported: true`.
- API key value is not present in the output.

Call `web_search` with a harmless query:

```json
{
  "query": "example domain",
  "limit": 3,
  "provider": "auto"
}
```

Expected:

- `ok: true`,
- `provider: "brave"`,
- `count` is `0..3`,
- results, if any, include title, URL, snippet, rank, and provider.

If no provider key is available, verify `web_search` returns a clear `search_provider_unavailable` error and `list_search_providers` explains why.

## 6. fetch_url smoke test

Call `fetch_url` against a safe public page:

```json
{
  "url": "https://example.com/",
  "output_mode": "readability",
  "output_limit": 8000
}
```

Expected:

- `ok: true`,
- final URL/status/content type metadata,
- capped text output,
- no private/localhost target access.

Negative check:

```json
{
  "url": "http://127.0.0.1:9222/json/version",
  "output_mode": "headers"
}
```

Expected:

- rejected as unsafe/private/localhost target.

## 7. Browser no-live status smoke test

With no browser listening on the configured CDP port:

```json
{}
```

Call `browser_status`.

Expected:

- `ok: true`, because status itself is a diagnostic tool,
- browser status reports `reachable: false` and `attached: false`,
- actionable instructions mention starting a browser with `--remote-debugging-port` and localhost binding.

Call `browser_attach`.

Expected:

- error payload with code such as `browser_attach_failed`,
- useful hint about CDP/remote debugging.

## 8. Optional browser launch/attach smoke test

Only run this when local browser automation is acceptable.

Call `browser_launch` with no profile arguments, or manually launch Chrome/Edge/Brave with a temporary user-data-dir:

```powershell
& "C:\Program Files\Google\Chrome\Application\chrome.exe" `
  --remote-debugging-address=127.0.0.1 `
  --remote-debugging-port=9222 `
  --user-data-dir="$env:TEMP\nullbot-chrome-profile"
```

Then call:

1. `browser_status`
2. `browser_attach`
3. `browser_navigate` to `https://example.com/`
4. `browser_read` with `mode: "readability"`
5. `browser_query` with `selector: "a"`
6. `browser_screenshot` to `smoke/example.png`

Expected:

- CDP endpoint is loopback only.
- Attach succeeds.
- Navigate/read/query return page metadata and capped output.
- Screenshot writes a valid PNG under the workspace.

Do not perform purchase, send, delete, account-change, private-data submission, or other consequential-action smoke tests.

## 9. Eval gate smoke test

With the default server configuration, call `browser_eval` with a simple expression.

Expected:

- returns `browser_eval_disabled`,
- message tells the user to restart with `--allow-eval=true` if explicitly needed.

Only test enabled eval after explicit approval and on a harmless page/profile.

## 10. Release artifact smoke notes

When `scripts\release.ps1` is run in a later step, record:

- version,
- artifact directory,
- normal binary size,
- small/UPX binary size if generated,
- `SHA256SUMS.txt` contents,
- test results,
- any skipped optional smoke tests and why,
- known limitations or pending decisions before publish.
