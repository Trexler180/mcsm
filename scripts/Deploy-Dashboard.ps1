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

    Host configuration outside /srv/dashboard — the nginx snippets, systemd
    drop-ins, the polkit reboot grant, the SSH watchdog — lives in deploy/ and
    is applied by deploy/provision.sh. Every deploy ships that bundle and
    reports drift between the host and the repo; -Provision (or -Part host)
    converges the host to it. Secrets stay in /srv/dashboard/data/secrets.env
    and are never read or written by this script.

.EXAMPLE
    .\Deploy-Dashboard.ps1                # build + deploy everything
    .\Deploy-Dashboard.ps1 -Part web      # web only (fast iteration loop)
    .\Deploy-Dashboard.ps1 -Part binaries # api + agent only
    .\Deploy-Dashboard.ps1 -Part host     # host config only (provision)
    .\Deploy-Dashboard.ps1 -Provision     # deploy everything + converge host config
    .\Deploy-Dashboard.ps1 -Test          # run tests/lint first, then deploy
    .\Deploy-Dashboard.ps1 -Rollback      # restore previous binaries + web

.NOTES
    A new non-secret setting belongs in a deploy/systemd drop-in; a new secret
    goes in /srv/dashboard/data/secrets.env by hand, then redeploy.
#>
[CmdletBinding()]
param(
    [ValidateSet('all', 'web', 'api', 'agent', 'binaries', 'host')]
    [string]$Part = 'all',
    [switch]$Test,
    [switch]$Provision,
    [switch]$Rollback,
    [string]$RepoPath = 'E:\Coding\ServerManager'
)

$ErrorActionPreference = 'Stop'

$Remote     = Join-Path $PSScriptRoot 'Invoke-Remote.ps1'
$ApplyLocal = Join-Path $PSScriptRoot 'deploy-apply.sh'
$Stage      = Join-Path $env:TEMP 'mcsm-deploy'
$WebBase    = '/dashboard/'
# Root-only staging dir for uploads. Never /srv/dashboard/data: that belongs to
# the mcsm user, and the applier runs as root (see deploy-apply.sh).
$RemoteStage = '/root/mcsm-deploy'
$ProvisionSrc = Join-Path $RepoPath 'deploy'

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
if (-not (Test-Path (Join-Path $ProvisionSrc 'provision.sh'))) { throw "deploy/provision.sh not found under $RepoPath" }
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
    Invoke-RemoteCmd "install -d -m 700 $RemoteStage"
    Send-RemoteFile $applyStaged "$RemoteStage/deploy-apply.sh"
    Invoke-RemoteCmd "bash $RemoteStage/deploy-apply.sh rollback sha:rollback"
    Write-Host "`nRollback complete." -ForegroundColor Green
    return
}

$doWeb   = $Part -in @('all', 'web')
$doApi   = $Part -in @('all', 'api', 'binaries')
$doAgent = $Part -in @('all', 'agent', 'binaries')
$doProvision = $Provision -or $Part -eq 'host'

# Bash for the provisioning tests: Git for Windows' own, never the WSL launcher
# that System32\bash.exe would resolve to.
function Find-GitBash {
    foreach ($c in @("$env:ProgramFiles\Git\bin\bash.exe", "${env:ProgramFiles(x86)}\Git\bin\bash.exe", "$env:LOCALAPPDATA\Programs\Git\bin\bash.exe")) {
        if ($c -and (Test-Path $c)) { return $c }
    }
    return $null
}

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
    Step "provision tests"
    $gitBash = Find-GitBash
    if (-not $gitBash) { throw "provision tests need Git for Windows' bash.exe" }
    & $gitBash "$ProvisionSrc/provision_test.sh"
    if ($LASTEXITCODE) { throw "provision tests failed" }
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

if ($parts.Count -eq 0 -and -not $doProvision) { throw "nothing to deploy" }

# --- Package host provisioning ---------------------------------------------
# Shipped with every deploy: the applier always checks the host against it,
# and applies it when asked. Text is normalized to LF (bash, systemd, and
# nginx all read these) and tests stay behind.
$provStage = Join-Path $Stage 'provision'
if (Test-Path $provStage) { Remove-Item -Recurse -Force $provStage }
$utf8 = New-Object System.Text.UTF8Encoding($false)
Get-ChildItem $ProvisionSrc -Recurse -File | Where-Object { $_.Name -notlike '*_test.sh' } | ForEach-Object {
    $rel = $_.FullName.Substring($ProvisionSrc.Length).TrimStart('\', '/')
    $dest = Join-Path $provStage $rel
    New-Item -ItemType Directory -Force -Path (Split-Path $dest) | Out-Null
    $text = [IO.File]::ReadAllText($_.FullName) -replace "`r`n", "`n" -replace "`r", "`n"
    [IO.File]::WriteAllText($dest, $text, $utf8)
}
$provTgz = Join-Path $Stage 'provision.tgz'
if (Test-Path $provTgz) { Remove-Item $provTgz }
tar -czf $provTgz -C $provStage .
if ($LASTEXITCODE) { throw "packaging host provisioning failed" }

$applyArgs = @($parts)
if ($doProvision) { $applyArgs += 'provision' }
$applyArgs += @("base:$WebBase", "sha:$sha")
$what = @($parts) + $(if ($doProvision) { @('host') } else { @() })

# --- Upload ----------------------------------------------------------------
Step "Uploading artifacts ($($what -join ', '))"
Invoke-RemoteCmd "install -d -m 700 $RemoteStage"
Send-RemoteFile $applyStaged "$RemoteStage/deploy-apply.sh"
Send-RemoteFile $provTgz     "$RemoteStage/provision.tgz"
if ($parts -contains 'web')   { Send-RemoteFile (Join-Path $Stage 'web.tgz')    "$RemoteStage/web.tgz" }
if ($parts -contains 'api')   { Send-RemoteFile (Join-Path $Stage 'mcsm-api')   "$RemoteStage/upload-mcsm-api" }
if ($parts -contains 'agent') { Send-RemoteFile (Join-Path $Stage 'mcsm-agent') "$RemoteStage/upload-mcsm-agent" }

# --- Apply -----------------------------------------------------------------
Step "Applying on server (sha=$sha)"
Invoke-RemoteCmd ("bash $RemoteStage/deploy-apply.sh " + ($applyArgs -join ' '))

Write-Host "`nDeploy complete: $($what -join ', ') @ $sha" -ForegroundColor Green
