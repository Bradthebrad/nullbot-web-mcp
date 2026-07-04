param(
    [string]$Version = "v0.1.0",
    [switch]$Publish,
    [switch]$UpdateExisting,
    [switch]$NoCompress,
    [string]$Repo = "Bradthebrad/nullbot-web-mcp",
    [string]$UpxPath = "C:\tmp\upx-5.1.1-win64\upx.exe",
    [string]$GhPath = "C:\Program Files\GitHub CLI\gh.exe"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
$outDir = Join-Path $root "dist\releases\$Version"
$cacheRoot = Join-Path ([System.IO.Path]::GetTempPath()) "nullbot-web-mcp-go-build"
$gocache = Join-Path $cacheRoot "gocache"
$gomodcache = Join-Path $cacheRoot "gomodcache"

New-Item -ItemType Directory -Force -Path $outDir, $gocache, $gomodcache | Out-Null

$normal = Join-Path $outDir "nullbot-web-mcp.exe"
$small = Join-Path $outDir "nullbot-web-mcp-small.exe"

$oldGoCache = $env:GOCACHE
$oldGoModCache = $env:GOMODCACHE
try {
    $env:GOCACHE = $gocache
    $env:GOMODCACHE = $gomodcache

    Push-Location $root
    try {
        go test ./...
        if ($LASTEXITCODE -ne 0) {
            throw "go test failed with exit code $LASTEXITCODE"
        }

        go build -trimpath -ldflags "-s -w" -o $normal ./cmd/nullbot-web-mcp
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed with exit code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }
} finally {
    $env:GOCACHE = $oldGoCache
    $env:GOMODCACHE = $oldGoModCache
}

if (Test-Path -LiteralPath $small) {
    Remove-Item -LiteralPath $small -Force
}

if (-not $NoCompress) {
    Copy-Item -LiteralPath $normal -Destination $small -Force
    if (-not (Test-Path -LiteralPath $UpxPath)) {
        throw "UPX not found at $UpxPath. Pass -NoCompress or provide -UpxPath."
    }
    & $UpxPath --best --lzma $small
    if ($LASTEXITCODE -ne 0) {
        throw "UPX failed with exit code $LASTEXITCODE"
    }
}

$assets = @($normal)
if (Test-Path -LiteralPath $small) {
    $assets += $small
}

$checksum = Join-Path $outDir "SHA256SUMS.txt"
Get-FileHash -LiteralPath $assets -Algorithm SHA256 |
    ForEach-Object { "$($_.Hash.ToLower())  $(Split-Path -Leaf $_.Path)" } |
    Set-Content -LiteralPath $checksum -Encoding ascii

$notes = Join-Path $outDir "RELEASE_NOTES.md"
$smallNote = if (Test-Path -LiteralPath $small) {
    "- ``nullbot-web-mcp-small.exe``: optional UPX-compressed Windows MCP server."
} else {
    "- ``nullbot-web-mcp-small.exe`` was not generated for this local build."
}

@"
# NullBot Web MCP $Version

This local release package includes Windows artifacts for ``nullbot-web-mcp``.

## Assets

- ``nullbot-web-mcp.exe``: normal uncompressed Windows MCP server.
$smallNote
- ``SHA256SUMS.txt``: SHA256 checksums for generated executable assets.

## What This MCP Server Provides

- Web search through provider adapters. Brave Search is implemented in this build.
- Safe public URL fetching with byte caps, redirect validation, and HTML readability extraction.
- Native Chromium-family browser automation through localhost CDP attach/launch.
- Browser status, attach, launch, navigate, read, screenshot, query, click, type, tab, and gated eval tools.
- ``stdio`` by default, with ``streamable-http``, ``http``, and ``sse`` transports available.

## Permissions / Safety Notes

- ``network_fetch``: search, fetch, and browser navigation may contact external websites.
- ``browser_control``: browser tools can inspect and operate local Chromium-family browser sessions. Confirm before purchases, sends, deletes, account changes, private-data submission, or other consequential actions.
- ``workspace_write``: screenshots write PNG files only under the configured workspace.
- CDP endpoints are restricted to localhost loopback addresses.
- ``browser_eval`` is disabled unless the server starts with ``--allow-eval=true``.

## UPX Small Build Note

UPX-compressed Windows binaries can occasionally be flagged or blocked by antivirus or SmartScreen heuristics because packed executables are harder for scanners to inspect.

If Windows blocks the small build, use the normal ``nullbot-web-mcp.exe`` instead.

## Smoke Testing

See ``docs/SMOKE_TESTS.md`` in the repository for the pre-publish smoke-test checklist.

## Publishing Gate

This script only creates local artifacts. Creating a GitHub release, editing marketplace metadata, installing packages, or enabling MCP servers requires a separate explicit approval step.
"@ | Set-Content -LiteralPath $notes -Encoding utf8

Write-Host "Release files written to $outDir"
Get-ChildItem -LiteralPath $outDir | Select-Object Name, Length

if ($Publish) {
    if (-not (Test-Path -LiteralPath $GhPath)) {
        $cmd = Get-Command gh -ErrorAction SilentlyContinue
        if ($null -eq $cmd) {
            throw "GitHub CLI not found. Install gh or pass -GhPath."
        }
        $GhPath = $cmd.Source
    }

    $releaseExists = $false
    $oldErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        & $GhPath release view $Version --repo $Repo *> $null
        $releaseViewExitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $oldErrorActionPreference
    }
    if ($releaseViewExitCode -eq 0) {
        $releaseExists = $true
    }

    $uploadAssets = @()
    $uploadAssets += $assets
    $uploadAssets += $checksum

    if ($releaseExists) {
        if (-not $UpdateExisting) {
            throw "Release $Version already exists. Re-run with -UpdateExisting to replace assets."
        }
        & $GhPath release upload $Version @uploadAssets --repo $Repo --clobber
        if ($LASTEXITCODE -ne 0) {
            throw "gh release upload failed with exit code $LASTEXITCODE"
        }
    } else {
        & $GhPath release create $Version @uploadAssets --repo $Repo --title "NullBot Web MCP $Version" --notes-file $notes
        if ($LASTEXITCODE -ne 0) {
            throw "gh release create failed with exit code $LASTEXITCODE"
        }
    }
}
