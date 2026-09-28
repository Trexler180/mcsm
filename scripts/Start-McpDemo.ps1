<#
.SYNOPSIS
    Runs the API locally with remote-agent (MCP over OAuth) access enabled, and
    prints the exact command to connect a client.

.DESCRIPTION
    This is a local development helper, not deployment tooling. It starts the
    API in the foreground with a loopback public origin so the OAuth
    authorization server can state its own address, then prints the one command
    an operator runs to connect Claude Code or Codex.

    Deliberate properties:

      * It never writes to a global agent configuration. It prints a command;
        running it is the operator's decision.
      * It bakes no credential into any file. Connecting is a browser click, and
        the token goes straight from the token endpoint to the client.
      * The public origin is the Vite dev server, because that is the single
        origin the browser, the SPA and the MCP endpoint all share in local
        development. Vite forwards /api and the /.well-known documents to the
        API (see apps/web/vite.config.ts).

.PARAMETER WebPort
    Port the Vite dev server is on. Must match `pnpm dev`.

.PARAMETER ApiPort
    Port the API binds. Must match the proxy target in apps/web/vite.config.ts.

.EXAMPLE
    # Terminal 1
    make dev-web
    # Terminal 2
    .\scripts\Start-McpDemo.ps1
#>
[CmdletBinding()]
param(
    [int]$WebPort = 3000,
    [int]$ApiPort = 8081
)

$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path -Parent $PSScriptRoot
$apiDir = Join-Path $repoRoot 'apps/api'
if (-not (Test-Path $apiDir)) {
    throw "Cannot find apps/api under $repoRoot"
}

# Everything the browser, the SPA and the client agree on is this one origin.
# http is acceptable only because it is loopback, which the origin resolver
# enforces independently.
$origin = "http://localhost:$WebPort"
$resource = "$origin/api/v1/mcp"

Write-Host ''
Write-Host 'ServerManager — remote agent (MCP) demo' -ForegroundColor Cyan
Write-Host ('-' * 54)

# The API is useless for this demo without the SPA: the consent screen is an
# authenticated route in the dashboard, and the whole flow goes through it.
$webUp = $false
try {
    $probe = Invoke-WebRequest -Uri $origin -TimeoutSec 2 -UseBasicParsing
    $webUp = $probe.StatusCode -eq 200
} catch {
    $webUp = $false
}
if (-not $webUp) {
    Write-Host "The dashboard is not answering on $origin." -ForegroundColor Yellow
    Write-Host 'Start it first, in another terminal:' -ForegroundColor Yellow
    Write-Host '    make dev-web' -ForegroundColor Yellow
    Write-Host ''
    Write-Host 'Continuing anyway — the API will start, but the consent screen'
    Write-Host 'will not load until the dashboard is running.'
    Write-Host ''
}

Write-Host 'Connect a client with one of these, then approve in the browser:'
Write-Host ''
Write-Host "    claude mcp add --transport http servermanager $resource" -ForegroundColor Green
Write-Host ''
Write-Host '  Codex — add to ~/.codex/config.toml:'
Write-Host ''
Write-Host '    [mcp_servers.servermanager]' -ForegroundColor Green
Write-Host "    url = `"$resource`"" -ForegroundColor Green
Write-Host ''
Write-Host 'No token appears in either snippet, and none is written anywhere.'
Write-Host 'Sign in to the dashboard first so the consent screen has a session.'
Write-Host 'Revoke from Account -> Security -> Remote agent connections.'
Write-Host ('-' * 54)
Write-Host ''

Push-Location $apiDir
try {
    $env:MCSM_DEV_MODE = '1'
    $env:DATABASE_PATH = './mcsm.db'
    # A fixed dev secret keeps dashboard sessions alive across restarts. It is a
    # well-known development value and must never reach a deployment.
    $env:JWT_SECRET = 'dev-secret'
    $env:API_PORT = "$ApiPort"
    # Pin the canonical origin: Vite proxies with changeOrigin, so the API would
    # otherwise see its own host and mint tokens bound to the wrong audience.
    $env:MCP_PUBLIC_ORIGIN = $origin
    $env:APP_BASE_PATH = '/'

    go run ./cmd/server
} finally {
    Pop-Location
}
