<#
.SYNOPSIS
    Build and deploy ServerManager to the contained /dashboard test instance.

.DESCRIPTION
    Builds the web bundle (subpath base + self-destruct service worker) and/or
    the Linux api/agent binaries from the local repo, uploads them through
    Invoke-Remote.ps1 (which keeps the host, credentials, and domain hidden),
    and applies them on the VPS: near-atomic web swap, binary backup for
    rollback, service restart (the api restart auto-applies DB migrations), and
    a health check. Nothing is deployed unless every build step succeeds.

.EXAMPLE
    .\Deploy-Dashboard.ps1                # build + deploy everything
    .\Deploy-Dashboard.ps1 -Part web      # web only (fast iteration loop)
    .\Deploy-Dashboard.ps1 -Part binaries # api + agent only
    .\Deploy-Dashboard.ps1 -Test          # run tests/lint first, then deploy
    .\Deploy-Dashboard.ps1 -Rollback      # restore previous binaries + web

.NOTES
    Config (secrets, systemd units, nginx) is provisioned once and is NOT touched
    by this script. If a release needs a new env var, set it in
    /srv/dashboard/data/secrets.env manually, then redeploy.
#>
[CmdletBinding()]
param(
    [ValidateSet('all', 'web', 'api', 'agent', 'binaries')]
    [string]$Part = 'all',
    [switch]$Test,
    [switch]$Rollback,
    [string]$RepoPath = 'E:\Coding\ServerManager'
)

$ErrorActionPreference = 'Stop'

$Remote     = Join-Path $PSScriptRoot 'Invoke-Remote.ps1'
$ApplyLocal = Join-Path $PSScriptRoot 'deploy-apply.sh'
$Stage      = Join-Path $env:TEMP 'mcsm-deploy'
$WebBase    = '/dashboard/'
$RemoteData = '/srv/dashboard/data'

function Step($m) { Write-Host "==> $m" -ForegroundColor Cyan }

function Invoke-RemoteCmd([string]$cmd) {
    & $Remote $cmd
    if ($LASTEXITCODE -ne 0) { throw "remote command failed (exit $LASTEXITCODE)" }
}
function Send-RemoteFile([string]$local, [string]$dest) {
    & $Remote -Put -LocalPath $local -RemotePath $dest
    if ($LASTEXITCODE -ne 0) { throw "upload failed: $local" }
}

if (-not (Test-Path $Remote))     { throw "Invoke-Remote.ps1 not found at $Remote" }
if (-not (Test-Path $ApplyLocal)) { throw "deploy-apply.sh not found at $ApplyLocal" }
if (-not (Test-Path $RepoPath))   { throw "repo not found at $RepoPath" }
New-Item -ItemType Directory -Force -Path $Stage | Out-Null

# Upload the applier with normalized LF endings + no BOM (bash-safe), every run.
$applyStaged = Join-Path $Stage 'deploy-apply.sh'
$applyText = (Get-Content $ApplyLocal -Raw) -replace "`r`n", "`n" -replace "`r", "`n"
[IO.File]::WriteAllText($applyStaged, $applyText, (New-Object System.Text.UTF8Encoding($false)))

# Git SHA for the audit trail (best-effort).
$sha = (& git -C $RepoPath rev-parse --short HEAD 2>$null)
if (-not $sha) { $sha = 'nogit' }
if (& git -C $RepoPath status --porcelain 2>$null) { $sha = "$sha+dirty" }

# --- Rollback path ---------------------------------------------------------
if ($Rollback) {
    Step "Rolling back to previous artifacts"
    Send-RemoteFile $applyStaged "$RemoteData/deploy-apply.sh"
    Invoke-RemoteCmd "bash $RemoteData/deploy-apply.sh rollback sha:rollback"
    Write-Host "`nRollback complete." -ForegroundColor Green
    return
}

$doWeb   = $Part -in @('all', 'web')
$doApi   = $Part -in @('all', 'api', 'binaries')
$doAgent = $Part -in @('all', 'agent', 'binaries')

# --- Optional tests (gate the deploy) --------------------------------------
if ($Test) {
    if ($doWeb) {
        Step "Web typecheck + lint"
        Push-Location "$RepoPath\apps\web"
        try {
            pnpm run typecheck; if ($LASTEXITCODE) { throw "web typecheck failed" }
            pnpm run lint;      if ($LASTEXITCODE) { throw "web lint failed" }
        } finally { Pop-Location }
    }
    if ($doApi) {
        Step "go test (api)"
        Push-Location "$RepoPath\apps\api"
        try { go test ./...; if ($LASTEXITCODE) { throw "api tests failed" } } finally { Pop-Location }
    }
    if ($doAgent) {
        Step "go test (agent)"
        Push-Location "$RepoPath\apps\agent"
        try { go test ./...; if ($LASTEXITCODE) { throw "agent tests failed" } } finally { Pop-Location }
    }
}

$parts = @()

# --- Build web -------------------------------------------------------------
if ($doWeb) {
    Step "Building web bundle (base=$WebBase, self-destruct SW)"
    Push-Location "$RepoPath\apps\web"
    try {
        $env:VITE_BASE = $WebBase
        $env:VITE_PWA_SELF_DESTROY = '1'
        pnpm run build
        if ($LASTEXITCODE) { throw "web build failed" }
    } finally {
        Remove-Item Env:\VITE_BASE -ErrorAction SilentlyContinue
        Remove-Item Env:\VITE_PWA_SELF_DESTROY -ErrorAction SilentlyContinue
        Pop-Location
    }
    $tgz = Join-Path $Stage 'web.tgz'
    if (Test-Path $tgz) { Remove-Item $tgz }
    tar -czf $tgz -C "$RepoPath\apps\web\dist" .
    if ($LASTEXITCODE) { throw "packaging web bundle failed" }
    $parts += 'web'
}

# --- Build Linux binaries --------------------------------------------------
if ($doApi -or $doAgent) {
    Step "Cross-compiling Linux binaries (CGO_ENABLED=0, linux/amd64)"
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    try {
        if ($doApi) {
            Push-Location "$RepoPath\apps\api"
            try {
                go build -trimpath -ldflags='-s -w' -o (Join-Path $Stage 'mcsm-api') ./cmd/server
                if ($LASTEXITCODE) { throw "api build failed" }
            } finally { Pop-Location }
            $parts += 'api'
        }
        if ($doAgent) {
            Push-Location "$RepoPath\apps\agent"
            try {
                go build -trimpath -ldflags='-s -w' -o (Join-Path $Stage 'mcsm-agent') ./cmd/agent
                if ($LASTEXITCODE) { throw "agent build failed" }
            } finally { Pop-Location }
            $parts += 'agent'
        }
    } finally {
        Remove-Item Env:\GOOS, Env:\GOARCH, Env:\CGO_ENABLED -ErrorAction SilentlyContinue
    }
}

if ($parts.Count -eq 0) { throw "nothing to deploy" }

# --- Upload ----------------------------------------------------------------
Step "Uploading artifacts ($($parts -join ', '))"
Send-RemoteFile $applyStaged "$RemoteData/deploy-apply.sh"
if ($parts -contains 'web')   { Send-RemoteFile (Join-Path $Stage 'web.tgz')    "$RemoteData/web.tgz" }
if ($parts -contains 'api')   { Send-RemoteFile (Join-Path $Stage 'mcsm-api')   "$RemoteData/upload-mcsm-api" }
if ($parts -contains 'agent') { Send-RemoteFile (Join-Path $Stage 'mcsm-agent') "$RemoteData/upload-mcsm-agent" }

# --- Apply -----------------------------------------------------------------
Step "Applying on server (sha=$sha)"
Invoke-RemoteCmd ("bash $RemoteData/deploy-apply.sh " + ($parts -join ' ') + " sha:$sha")

Write-Host "`nDeploy complete: $($parts -join ', ') @ $sha" -ForegroundColor Green
