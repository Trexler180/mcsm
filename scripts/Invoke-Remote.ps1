<#
.SYNOPSIS
    Runs a command on a remote host over SSH without exposing the host,
    username, or password to whoever (or whatever) invokes the script.

.DESCRIPTION
    Connection details are stored encrypted with Windows DPAPI (tied to the
    Windows user account that runs -Setup) in a hidden sidecar file next to
    this script. They are never printed. ALL output is run through a redactor
    so the host/user cannot leak even in an error message.

    Designed so an AI agent can call:
        powershell -File Invoke-Remote.ps1 "<remote command>"
    and receive only the command's stdout/stderr, never the credentials.

.EXAMPLE
    # One-time setup (run as the human admin):
    .\Invoke-Remote.ps1 -Setup

    # Normal use (what the agent runs):
    .\Invoke-Remote.ps1 "uptime"
    .\Invoke-Remote.ps1 "tail -n 20 /opt/minecraft/logs/latest.log"

.NOTES
    Requires plink.exe (PuTTY). Install with:  winget install PuTTY.PuTTY
#>

[CmdletBinding(DefaultParameterSetName = 'Run')]
param(
    [Parameter(ParameterSetName = 'Setup')]
    [switch]$Setup,

    [Parameter(ParameterSetName = 'Run', ValueFromRemainingArguments = $true, Position = 0)]
    [string[]]$Command,

    # Upload a local file to the remote host without exposing credentials.
    #   .\Invoke-Remote.ps1 -Put <localPath> <remotePath>
    [Parameter(ParameterSetName = 'Put', Mandatory = $true)]
    [switch]$Put,
    [Parameter(ParameterSetName = 'Put', Mandatory = $true)]
    [string]$LocalPath,
    [Parameter(ParameterSetName = 'Put', Mandatory = $true)]
    [string]$RemotePath
)

$ErrorActionPreference = 'Stop'
$ConfigPath = Join-Path $PSScriptRoot '.connection.cfg'

# --- helpers ---------------------------------------------------------------

function Find-Plink {
    $candidates = @(
        'plink.exe',
        "$env:ProgramFiles\PuTTY\plink.exe",
        "${env:ProgramFiles(x86)}\PuTTY\plink.exe",
        "$env:LOCALAPPDATA\Programs\PuTTY\plink.exe"
    )
    foreach ($c in $candidates) {
        $cmd = Get-Command $c -ErrorAction SilentlyContinue
        if ($cmd) { return $cmd.Source }
    }
    throw "plink.exe not found. Install PuTTY:  winget install PuTTY.PuTTY"
}

function Find-Pscp {
    $candidates = @(
        'pscp.exe',
        "$env:ProgramFiles\PuTTY\pscp.exe",
        "${env:ProgramFiles(x86)}\PuTTY\pscp.exe",
        "$env:LOCALAPPDATA\Programs\PuTTY\pscp.exe"
    )
    foreach ($c in $candidates) {
        $cmd = Get-Command $c -ErrorAction SilentlyContinue
        if ($cmd) { return $cmd.Source }
    }
    throw "pscp.exe not found. Install PuTTY:  winget install PuTTY.PuTTY"
}

function Protect-Text([string]$Plain) {
    # DPAPI (CurrentUser) encrypt -> opaque string
    ConvertFrom-SecureString (ConvertTo-SecureString $Plain -AsPlainText -Force)
}

function Unprotect-Text([string]$Enc) {
    $sec = ConvertTo-SecureString $Enc
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec)
    try   { [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr) }
    finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr) }
}

# --- setup mode ------------------------------------------------------------

if ($Setup) {
    $plink = Find-Plink

    Write-Host "=== Remote connection setup ===" -ForegroundColor Cyan
    $hostname = Read-Host "SSH host (address)"
    $port     = Read-Host "SSH port [22]"
    if ([string]::IsNullOrWhiteSpace($port)) { $port = '22' }
    $user     = Read-Host "SSH username"
    $secPass  = Read-Host "SSH password" -AsSecureString
    $plainPass = [Runtime.InteropServices.Marshal]::PtrToStringBSTR(
        [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secPass))

    # Capture the host key fingerprint by declining the trust prompt ('n').
    Write-Host "`nProbing host key..." -ForegroundColor DarkGray
    $probe = ('n' | & $plink -ssh -P $port "$user@$hostname" "exit" 2>&1 | Out-String)
    $fp = ([regex]::Match($probe, 'SHA256:[A-Za-z0-9+/=]+')).Value
    if (-not $fp) {
        $fp = ([regex]::Match($probe, '(ssh-[a-z0-9]+ \d+ [0-9a-f:]+)')).Value
    }
    if (-not $fp) {
        Write-Warning "Could not auto-detect host key fingerprint; will auto-accept on first run."
    } else {
        Write-Host "Host key: $fp" -ForegroundColor DarkGray
    }

    # Verify the credentials actually work before saving.
    Write-Host "Testing connection..." -ForegroundColor DarkGray
    $hostkeyArgs = @(); if ($fp) { $hostkeyArgs = @('-hostkey', $fp) }
    $test = (& $plink -ssh -batch -P $port @hostkeyArgs -pw $plainPass "$user@$hostname" "echo OK" 2>&1 | Out-String)
    if ($test -notmatch 'OK') {
        throw "Connection test failed. Nothing was saved.`n$($test -replace [regex]::Escape($hostname),'[host]')"
    }

    $cfg = [ordered]@{
        Host    = Protect-Text $hostname
        Port    = Protect-Text $port
        User    = Protect-Text $user
        Pass    = Protect-Text $plainPass
        HostKey = if ($fp) { Protect-Text $fp } else { '' }
    }
    $cfg | ConvertTo-Json | Set-Content -Path $ConfigPath -Encoding UTF8

    # Hide the config file from casual view.
    (Get-Item $ConfigPath -Force).Attributes = 'Hidden'

    # scrub plaintext password from memory
    $plainPass = $null; [GC]::Collect()

    Write-Host "`nSaved (encrypted) to $ConfigPath" -ForegroundColor Green
    Write-Host "Test:  .\$(Split-Path $PSCommandPath -Leaf) ""whoami""" -ForegroundColor Green
    return
}

# --- put (upload) mode -----------------------------------------------------

if ($Put) {
    if (-not (Test-Path $ConfigPath)) {
        Write-Error "Not configured. Run:  .\$(Split-Path $PSCommandPath -Leaf) -Setup"
        exit 3
    }
    if (-not (Test-Path $LocalPath)) {
        Write-Error "Local file not found: $LocalPath"
        exit 4
    }
    $pscp = Find-Pscp
    $cfg  = Get-Content $ConfigPath -Raw | ConvertFrom-Json

    $hostname = Unprotect-Text $cfg.Host
    $port     = Unprotect-Text $cfg.Port
    $user     = Unprotect-Text $cfg.User
    $pass     = Unprotect-Text $cfg.Pass
    $fp       = if ($cfg.HostKey) { Unprotect-Text $cfg.HostKey } else { '' }

    $pscpArgs = @('-batch', '-P', $port)
    if ($fp) { $pscpArgs += @('-hostkey', $fp) }
    $pscpArgs += @('-pw', $pass, $LocalPath, "$user@${hostname}:$RemotePath")

    $raw  = (& $pscp @pscpArgs 2>&1 | Out-String)
    $exit = $LASTEXITCODE

    $redacted = $raw
    $redacted = $redacted -replace [regex]::Escape($hostname), '[host]'
    $redacted = $redacted -replace [regex]::Escape($user), '[user]'
    if ($fp) { $redacted = $redacted -replace [regex]::Escape($fp), '[hostkey]' }

    $pass = $null; $hostname = $null; $user = $null; [GC]::Collect()

    Write-Output $redacted.TrimEnd()
    if ($exit -eq 0) { Write-Output "[upload OK] -> $RemotePath" }
    exit $exit
}

# --- run mode --------------------------------------------------------------

if (-not $Command -or $Command.Count -eq 0) {
    Write-Error "Usage: .\$(Split-Path $PSCommandPath -Leaf) ""<remote command>""   (or -Setup first)"
    exit 2
}
if (-not (Test-Path $ConfigPath)) {
    Write-Error "Not configured. Run:  .\$(Split-Path $PSCommandPath -Leaf) -Setup"
    exit 3
}

$plink = Find-Plink
$cfg   = Get-Content $ConfigPath -Raw | ConvertFrom-Json

$hostname = Unprotect-Text $cfg.Host
$port     = Unprotect-Text $cfg.Port
$user     = Unprotect-Text $cfg.User
$pass     = Unprotect-Text $cfg.Pass
$fp       = if ($cfg.HostKey) { Unprotect-Text $cfg.HostKey } else { '' }

$remoteCommand = ($Command -join ' ')

# Build args. -batch = never prompt (non-interactive). -hostkey pins the key
# so it works regardless of which Windows account/registry cache is in play.
$plinkArgs = @('-ssh', '-batch', '-P', $port)
if ($fp) { $plinkArgs += @('-hostkey', $fp) }
$plinkArgs += @('-pw', $pass, "$user@$hostname", $remoteCommand)

$raw = (& $plink @plinkArgs 2>&1 | Out-String)
$exit = $LASTEXITCODE

# Redact anything that could leak the connection identity, even on error.
$redacted = $raw
$redacted = $redacted -replace [regex]::Escape($hostname), '[host]'
$redacted = $redacted -replace [regex]::Escape($user), '[user]'
if ($fp) { $redacted = $redacted -replace [regex]::Escape($fp), '[hostkey]' }

# scrub secrets from memory
$pass = $null; $hostname = $null; $user = $null; [GC]::Collect()

Write-Output $redacted.TrimEnd()
exit $exit
