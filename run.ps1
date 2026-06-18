param(
    [int]$ApiPort = 8081,
    [int]$AgentPort = 8090,
    [int]$WebPort = 3000,
    [string]$BindHost = "0.0.0.0",
    [string]$PublicHost = "",
    [string]$AdminEmail = "admin@example.com",
    [string]$AdminPassword = "changeme",
    [string]$JwtSecret = "local-dev-jwt-secret-change-me",
    [string]$AgentToken = "dev-agent-token",
    [switch]$NoBackendWatch,
    [switch]$SkipInstall
)

$ErrorActionPreference = "Stop"

$Root = $PSScriptRoot
$ApiDir = Join-Path $Root "apps/api"
$AgentDir = Join-Path $Root "apps/agent"
$WebDir = Join-Path $Root "apps/web"
$ServerRoot = Join-Path $Root "servers"
$DatabasePath = Join-Path $ApiDir "mcsm.db"
$global:ServerManagerStopRequested = $false
$global:ServerManagerForceStopRequested = $false

function Require-Command {
    param([string]$Name)
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Required command '$Name' was not found on PATH."
    }
}

function Resolve-PublicHost {
    param([string]$FallbackHost)

    if (-not [string]::IsNullOrWhiteSpace($PublicHost)) {
        return $PublicHost
    }

    $addresses = @()
    try {
        $addresses = @(
            Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
                Where-Object {
                    $_.IPAddress -notlike "127.*" -and
                    $_.IPAddress -notlike "169.254.*" -and
                    $_.IPAddress -ne "0.0.0.0" -and
                    $_.AddressState -eq "Preferred" -and
                    $_.PrefixOrigin -ne "WellKnown"
                } |
                Sort-Object -Property InterfaceMetric, InterfaceIndex |
                Select-Object -ExpandProperty IPAddress
        )
    }
    catch {
        $addresses = @()
    }

    if ($addresses.Count -gt 0) {
        return $addresses[0]
    }

    if ($FallbackHost -eq "0.0.0.0") {
        return "localhost"
    }

    return $FallbackHost
}

function Resolve-LocalConnectHost {
    param([string]$HostName)

    if ($HostName -eq "0.0.0.0" -or $HostName -eq "::") {
        return "127.0.0.1"
    }

    return $HostName
}

function Join-ProcessArguments {
    param([string[]]$Arguments)

    $escaped = foreach ($argument in $Arguments) {
        if ($argument -match '[\s"]') {
            '"' + ($argument -replace '"', '\"') + '"'
        }
        else {
            $argument
        }
    }

    [string]::Join(" ", $escaped)
}

function Resolve-ProcessStartInfo {
    param(
        [string]$Name,
        [string[]]$Arguments
    )

    $commandInfo = Get-Command $Name -ErrorAction Stop
    $commandPath = $commandInfo.Path
    if (-not $commandPath) {
        $commandPath = $commandInfo.Source
    }
    if (-not $commandPath) {
        $commandPath = $Name
    }

    $extension = [System.IO.Path]::GetExtension($commandPath).ToLowerInvariant()
    if ($extension -in @(".cmd", ".bat")) {
        $cmdPath = $env:ComSpec
        if (-not $cmdPath) {
            $cmdPath = "cmd.exe"
        }

        return [pscustomobject]@{
            FileName = $cmdPath
            Arguments = "/d /s /c `"$(Join-ProcessArguments -Arguments (@($commandPath) + $Arguments))`""
        }
    }

    if ($extension -eq ".ps1") {
        $powerShell = (Get-Command "pwsh" -ErrorAction SilentlyContinue).Path
        if (-not $powerShell) {
            $powerShell = (Get-Command "powershell" -ErrorAction Stop).Path
        }

        return [pscustomobject]@{
            FileName = $powerShell
            Arguments = "-NoProfile -ExecutionPolicy Bypass -File $(Join-ProcessArguments -Arguments (@($commandPath) + $Arguments))"
        }
    }

    [pscustomobject]@{
        FileName = $commandPath
        Arguments = Join-ProcessArguments -Arguments $Arguments
    }
}

function Start-DevProcess {
    param(
        [string]$Name,
        [string]$WorkingDirectory,
        [hashtable]$Environment,
        [string[]]$Command
    )

    $arguments = @()
    if ($Command.Count -gt 1) {
        $arguments = $Command[1..($Command.Count - 1)]
    }

    $startInfo = Resolve-ProcessStartInfo -Name $Command[0] -Arguments $arguments
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo.FileName = $startInfo.FileName
    $process.StartInfo.Arguments = $startInfo.Arguments
    $process.StartInfo.WorkingDirectory = $WorkingDirectory
    $process.StartInfo.UseShellExecute = $false
    $process.StartInfo.RedirectStandardOutput = $true
    $process.StartInfo.RedirectStandardError = $true
    $process.StartInfo.CreateNoWindow = $true

    $processEnvironment = $process.StartInfo.Environment
    if ($null -eq $processEnvironment) {
        $processEnvironment = $process.StartInfo.EnvironmentVariables
    }
    if ($null -eq $processEnvironment) {
        throw "Unable to configure environment for '$Name'."
    }
    foreach ($key in $Environment.Keys) {
        $processEnvironment[$key] = $Environment[$key]
    }

    $eventPrefix = "ServerManager.$PID.$Name"
    $outputEventId = "$eventPrefix.output"
    $errorEventId = "$eventPrefix.error"
    Register-ObjectEvent -InputObject $process -EventName OutputDataReceived -SourceIdentifier $outputEventId | Out-Null
    Register-ObjectEvent -InputObject $process -EventName ErrorDataReceived -SourceIdentifier $errorEventId | Out-Null

    try {
        [void]$process.Start()
        $process.BeginOutputReadLine()
        $process.BeginErrorReadLine()
    }
    catch {
        Unregister-Event -SourceIdentifier $outputEventId -ErrorAction SilentlyContinue
        Unregister-Event -SourceIdentifier $errorEventId -ErrorAction SilentlyContinue
        throw
    }

    [pscustomobject]@{
        Name = $Name
        Process = $process
        WorkingDirectory = $WorkingDirectory
        Environment = $Environment
        Command = $Command
        OutputEventId = $outputEventId
        ErrorEventId = $errorEventId
        ExitReported = $false
    }
}

function Receive-DevOutput {
    param([pscustomobject]$DevProcess)

    foreach ($sourceIdentifier in @($DevProcess.OutputEventId, $DevProcess.ErrorEventId)) {
        $events = @(Get-Event -SourceIdentifier $sourceIdentifier -ErrorAction SilentlyContinue)
        foreach ($event in $events) {
            $line = $event.SourceEventArgs.Data
            if ($null -ne $line) {
                Write-Host "[$($DevProcess.Name)] $line"
            }
            Remove-Event -EventIdentifier $event.EventIdentifier -ErrorAction SilentlyContinue
        }
    }
}

function Wait-DevHttp {
    param(
        [string]$Name,
        [string]$Url,
        [object[]]$Processes,
        [int]$TimeoutSeconds = 90,
        [switch]$AllowExited
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        foreach ($devProcess in $Processes) {
            Receive-DevOutput -DevProcess $devProcess
        }

        if (-not $AllowExited) {
            $exited = @($Processes | Where-Object { $_ -and $_.Process.HasExited })
            if ($exited.Count -gt 0) {
                foreach ($devProcess in $exited) {
                    Receive-DevOutput -DevProcess $devProcess
                }
                throw "$($exited[0].Name) exited before $Name became ready."
            }
        }

        try {
            $response = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 2 -ErrorAction Stop
            if ([int]$response.StatusCode -ge 200 -and [int]$response.StatusCode -lt 500) {
                return
            }
        }
        catch {
            Start-Sleep -Milliseconds 250
        }
    }

    throw "Timed out waiting for $Name at $Url."
}

function Stop-DevProcess {
    param([pscustomobject]$DevProcess)

    if (-not $DevProcess) {
        return
    }

    Receive-DevOutput -DevProcess $DevProcess

    if (-not $DevProcess.Process.HasExited) {
        Stop-ProcessTree -ProcessId $DevProcess.Process.Id
        [void]$DevProcess.Process.WaitForExit(1000)
    }

    Receive-DevOutput -DevProcess $DevProcess
    Unregister-Event -SourceIdentifier $DevProcess.OutputEventId -ErrorAction SilentlyContinue
    Unregister-Event -SourceIdentifier $DevProcess.ErrorEventId -ErrorAction SilentlyContinue
}

function Stop-DevProcesses {
    param([object[]]$Processes)

    if (-not $Processes) {
        return
    }

    foreach ($devProcess in $Processes) {
        if (-not $devProcess) {
            continue
        }

        Stop-DevProcess -DevProcess $devProcess
    }
}

function Get-DevSourceFingerprint {
    param(
        [string]$Path,
        [string[]]$Extensions,
        [string[]]$FileNames
    )

    $latest = 0
    $count = 0
    $ignoredDirs = @(
        [System.IO.Path]::DirectorySeparatorChar + ".git" + [System.IO.Path]::DirectorySeparatorChar,
        [System.IO.Path]::DirectorySeparatorChar + "bin" + [System.IO.Path]::DirectorySeparatorChar,
        [System.IO.Path]::DirectorySeparatorChar + "dist" + [System.IO.Path]::DirectorySeparatorChar,
        [System.IO.Path]::DirectorySeparatorChar + "node_modules" + [System.IO.Path]::DirectorySeparatorChar
    )

    Get-ChildItem -LiteralPath $Path -Recurse -File -ErrorAction SilentlyContinue |
        Where-Object {
            $fullName = $_.FullName
            foreach ($ignoredDir in $ignoredDirs) {
                if ($fullName.Contains($ignoredDir)) {
                    return $false
                }
            }
            return ($Extensions -contains $_.Extension) -or ($FileNames -contains $_.Name)
        } |
        ForEach-Object {
            $count++
            if ($_.LastWriteTimeUtc.Ticks -gt $latest) {
                $latest = $_.LastWriteTimeUtc.Ticks
            }
        }

    return "${count}:${latest}"
}

function Restart-DevProcess {
    param(
        [pscustomobject]$DevProcess,
        [string]$Reason = "source changed"
    )

    Write-Host "[$($DevProcess.Name)] ${Reason}; restarting..."
    $name = $DevProcess.Name
    $workingDirectory = $DevProcess.WorkingDirectory
    $environment = $DevProcess.Environment
    $command = $DevProcess.Command

    Stop-DevProcess -DevProcess $DevProcess
    return Start-DevProcess -Name $name -WorkingDirectory $workingDirectory -Environment $environment -Command $command
}

function Get-StableDevSourceFingerprint {
    param(
        [string]$Path,
        [string[]]$Extensions,
        [string[]]$FileNames,
        [int]$QuietMilliseconds = 500
    )

    $first = Get-DevSourceFingerprint -Path $Path -Extensions $Extensions -FileNames $FileNames
    Start-Sleep -Milliseconds $QuietMilliseconds
    $second = Get-DevSourceFingerprint -Path $Path -Extensions $Extensions -FileNames $FileNames

    if ($first -ne $second) {
        Start-Sleep -Milliseconds $QuietMilliseconds
        return Get-DevSourceFingerprint -Path $Path -Extensions $Extensions -FileNames $FileNames
    }

    return $second
}

# Exponential backoff for crash-looping services: 2s, 4s, 8s, 16s, then 30s.
function Get-RestartDelaySeconds {
    param([int]$Failures)

    $exponent = [Math]::Min($Failures, 5)
    return [int][Math]::Min(30, [Math]::Pow(2, $exponent))
}

# Reinstalling web deps on every launch is slow on an exFAT drive, so only run
# pnpm install when package.json / the lockfile / the pnpm config actually
# changed since the last successful install. The stamp lives inside node_modules
# (so it's cleared on a clean install) and is content-hashed, so it stays valid
# when the drive moves between machines.
function Get-WebInstallStamp {
    param([string]$Path)

    $parts = foreach ($file in @("package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml")) {
        $manifest = Join-Path $Path $file
        if (Test-Path $manifest) { (Get-FileHash -LiteralPath $manifest -Algorithm SHA256).Hash } else { "missing" }
    }
    return [string]::Join(":", $parts)
}

function Test-WebInstallNeeded {
    param([string]$Path)

    if (-not (Test-Path (Join-Path $Path "node_modules"))) { return $true }
    $stampFile = Join-Path $Path "node_modules\.mcsm-install-stamp"
    if (-not (Test-Path $stampFile)) { return $true }
    return ((Get-Content -Raw -LiteralPath $stampFile -ErrorAction SilentlyContinue) -ne (Get-WebInstallStamp -Path $Path))
}

function Save-WebInstallStamp {
    param([string]$Path)

    $stampFile = Join-Path $Path "node_modules\.mcsm-install-stamp"
    Set-Content -LiteralPath $stampFile -Value (Get-WebInstallStamp -Path $Path) -NoNewline
}

# Dependency manifests only — vite hot-reloads web sources itself, but new or
# changed packages need a pnpm install plus a dev-server restart.
function Get-WebDepsFingerprint {
    param([string]$Path)

    $parts = foreach ($file in @("package.json", "pnpm-lock.yaml")) {
        $manifest = Join-Path $Path $file
        if (Test-Path $manifest) { (Get-Item $manifest).LastWriteTimeUtc.Ticks } else { 0 }
    }
    return [string]::Join(":", $parts)
}

function Stop-ProcessTree {
    param([int]$ProcessId)

    $process = Get-Process -Id $ProcessId -ErrorAction SilentlyContinue
    if (-not $process) {
        return
    }

    $children = Get-CimInstance Win32_Process -Filter "ParentProcessId = $ProcessId" -ErrorAction SilentlyContinue
    foreach ($child in $children) {
        Stop-ProcessTree -ProcessId $child.ProcessId
    }

    Stop-Process -Id $ProcessId -Force -ErrorAction SilentlyContinue
}

$cancelEventId = "ServerManager.$PID.cancel"
# A prior run that crashed before its finally block can leave this subscriber
# behind. Clear any same-id orphan so a re-run in the same session does not fail
# with SUBSCRIBER_EXISTS.
Get-EventSubscriber -SourceIdentifier $cancelEventId -ErrorAction SilentlyContinue | Unregister-Event -ErrorAction SilentlyContinue
$cancelSubscriber = Register-ObjectEvent -InputObject ([Console]) -EventName CancelKeyPress -SourceIdentifier $cancelEventId -Action {
    if ($global:ServerManagerStopRequested) {
        $global:ServerManagerForceStopRequested = $true
        return
    }

    $global:ServerManagerStopRequested = $true
    $EventArgs.Cancel = $true
}

Require-Command "go"
Require-Command "pnpm"

New-Item -ItemType Directory -Force -Path $ServerRoot | Out-Null

$ResolvedPublicHost = Resolve-PublicHost -FallbackHost $BindHost
$ApiConnectHost = Resolve-LocalConnectHost -HostName $BindHost

if (-not $SkipInstall) {
    if (Test-WebInstallNeeded -Path $WebDir) {
        Write-Host "[setup] Installing web dependencies..."
        Push-Location $WebDir
        try {
            pnpm install
            Save-WebInstallStamp -Path $WebDir
        }
        finally {
            Pop-Location
        }
    }
    else {
        Write-Host "[setup] Web dependencies up to date; skipping install."
    }
}

$ApiEnv = @{
    MCSM_DEV_MODE = "1"
    API_HOST = $BindHost
    API_PORT = "$ApiPort"
    DATABASE_PATH = $DatabasePath
    JWT_SECRET = $JwtSecret
    ADMIN_EMAIL = $AdminEmail
    ADMIN_PASSWORD = $AdminPassword
    RESET_ADMIN_PASSWORD = "1"
    SERVER_ROOT = $ServerRoot
    AUTO_REGISTER_LOCAL_AGENT = "1"
    LOCAL_AGENT_NAME = "Local Agent"
    LOCAL_AGENT_FQDN = "localhost"
    LOCAL_AGENT_PORT = "$AgentPort"
    LOCAL_AGENT_SCHEME = "http"
    LOCAL_AGENT_TOKEN = $AgentToken
}

$AgentEnv = @{
    MCSM_DEV_MODE = "1"
    AGENT_HOST = $BindHost
    AGENT_PORT = "$AgentPort"
    AGENT_TOKEN = $AgentToken
    AGENT_SERVER_ROOT = $ServerRoot
}

$WebEnv = @{
    VITE_API_HOST = $ApiConnectHost
    VITE_API_PORT = "$ApiPort"
    PORT = "$WebPort"
}

Write-Host ""
Write-Host "Starting ServerManager..."
Write-Host "  Web:   http://${ResolvedPublicHost}:$WebPort"
Write-Host "  API:   http://${ResolvedPublicHost}:$ApiPort"
Write-Host "  Agent: http://${ResolvedPublicHost}:$AgentPort"
Write-Host "  Bind:  $BindHost"
Write-Host "  Admin: $AdminEmail / $AdminPassword"
Write-Host "  Backend reload: $(if ($NoBackendWatch) { "off" } else { "on" })"
Write-Host ""
Write-Host "Press Ctrl+C to stop all services."
Write-Host ""

# Service definitions for the supervisor loop. Any service that exits — crash,
# port conflict, compile error during a reload — is restarted automatically
# with backoff, so updating the manager never requires stopping this script.
$specs = [ordered]@{
    api   = @{ Dir = $ApiDir;   Env = $ApiEnv;   Command = @("go", "run", "./cmd/server") }
    agent = @{ Dir = $AgentDir; Env = $AgentEnv; Command = @("go", "run", "./cmd/agent") }
    web   = @{ Dir = $WebDir;   Env = $WebEnv;   Command = @("pnpm", "dev", "--host", $BindHost, "--port", "$WebPort") }
}
$running = @{}
$health = @{}

function Start-Spec {
    param([string]$Name)

    $running[$Name] = Start-DevProcess -Name $Name -WorkingDirectory $specs[$Name].Dir -Environment $specs[$Name].Env -Command $specs[$Name].Command
    $health[$Name] = @{ Failures = 0; RestartAt = $null; StartedAt = Get-Date }
}

try {
    Start-Spec -Name "api"
    Start-Spec -Name "agent"
    try {
        Wait-DevHttp -Name "api" -Url "http://${ApiConnectHost}:$ApiPort/api/v1/health" -Processes @($running["api"], $running["agent"])
    }
    catch {
        Write-Host "[api] $($_.Exception.Message) Continuing; the supervisor will keep restarting it."
    }

    # The initial boot resets the dev admin password for convenience. Restarts
    # must not repeat that reset, because it deletes refresh tokens and forces
    # browser sessions to log in again.
    $ApiEnv["RESET_ADMIN_PASSWORD"] = "0"

    $apiExtensions = @(".go", ".sql")
    $apiFileNames = @("go.mod", "go.sum")
    $agentExtensions = @(".go")
    $agentFileNames = @("go.mod", "go.sum")
    $apiFingerprint = Get-StableDevSourceFingerprint -Path $ApiDir -Extensions $apiExtensions -FileNames $apiFileNames
    $agentFingerprint = Get-StableDevSourceFingerprint -Path $AgentDir -Extensions $agentExtensions -FileNames $agentFileNames
    $webDepsFingerprint = Get-WebDepsFingerprint -Path $WebDir
    $lastWatchCheck = Get-Date
    $watchReadyAt = (Get-Date).AddSeconds(5)

    Start-Spec -Name "web"

    while ($true) {
        if ($global:ServerManagerStopRequested -or $global:ServerManagerForceStopRequested) {
            break
        }

        foreach ($name in @($running.Keys)) {
            Receive-DevOutput -DevProcess $running[$name]
        }

        # Detect exits and schedule an automatic restart. A service that ran
        # for at least a minute resets its failure count, so an old crash does
        # not inflate the backoff of a fresh one.
        foreach ($name in @($running.Keys)) {
            $devProcess = $running[$name]
            $state = $health[$name]
            if ($devProcess.Process.HasExited -and -not $devProcess.ExitReported) {
                Receive-DevOutput -DevProcess $devProcess
                if (((Get-Date) - $state.StartedAt).TotalSeconds -ge 60) {
                    $state.Failures = 0
                }
                $state.Failures++
                $delay = Get-RestartDelaySeconds -Failures $state.Failures
                $state.RestartAt = (Get-Date).AddSeconds($delay)
                Write-Host "[$name] exited with code $($devProcess.Process.ExitCode); restarting in ${delay}s (Ctrl+C to stop)"
                $devProcess.ExitReported = $true
            }
        }

        foreach ($name in @($running.Keys)) {
            $state = $health[$name]
            if ($state.RestartAt -and (Get-Date) -ge $state.RestartAt) {
                $state.RestartAt = $null
                $state.StartedAt = Get-Date
                $running[$name] = Restart-DevProcess -DevProcess $running[$name] -Reason "recovering after exit"
            }
        }

        if (-not $NoBackendWatch -and (Get-Date) -ge $watchReadyAt) {
            $now = Get-Date
            if (($now - $lastWatchCheck).TotalMilliseconds -ge 1000) {
                $lastWatchCheck = $now

                $nextApiFingerprint = Get-DevSourceFingerprint -Path $ApiDir -Extensions $apiExtensions -FileNames $apiFileNames
                if ($nextApiFingerprint -ne $apiFingerprint) {
                    $apiFingerprint = Get-StableDevSourceFingerprint -Path $ApiDir -Extensions $apiExtensions -FileNames $apiFileNames
                    $running["api"] = Restart-DevProcess -DevProcess $running["api"]
                    $health["api"] = @{ Failures = 0; RestartAt = $null; StartedAt = Get-Date }
                }

                $nextAgentFingerprint = Get-DevSourceFingerprint -Path $AgentDir -Extensions $agentExtensions -FileNames $agentFileNames
                if ($nextAgentFingerprint -ne $agentFingerprint) {
                    $agentFingerprint = Get-StableDevSourceFingerprint -Path $AgentDir -Extensions $agentExtensions -FileNames $agentFileNames
                    $running["agent"] = Restart-DevProcess -DevProcess $running["agent"]
                    $health["agent"] = @{ Failures = 0; RestartAt = $null; StartedAt = Get-Date }
                }

                $nextWebDepsFingerprint = Get-WebDepsFingerprint -Path $WebDir
                if ($nextWebDepsFingerprint -ne $webDepsFingerprint) {
                    $webDepsFingerprint = $nextWebDepsFingerprint
                    Write-Host "[web] dependencies changed; running pnpm install..."
                    Push-Location $WebDir
                    try {
                        pnpm install
                        Save-WebInstallStamp -Path $WebDir
                    }
                    catch {
                        Write-Host "[web] pnpm install failed: $($_.Exception.Message)"
                    }
                    finally {
                        Pop-Location
                    }
                    $running["web"] = Restart-DevProcess -DevProcess $running["web"] -Reason "dependencies changed"
                    $health["web"] = @{ Failures = 0; RestartAt = $null; StartedAt = Get-Date }
                }
            }
        }

        Start-Sleep -Milliseconds 250
    }
}
finally {
    Write-Host ""
    Write-Host "Stopping ServerManager..."
    Stop-DevProcesses -Processes @($running.Values)
    Unregister-Event -SourceIdentifier $cancelEventId -ErrorAction SilentlyContinue
    Remove-Job -Job $cancelSubscriber -Force -ErrorAction SilentlyContinue
}
