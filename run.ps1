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

    # Force an array so zero args (a bare binary command) yields "" rather than a
    # null, and a single arg doesn't get char-split by [string]::Join.
    $escaped = @(foreach ($argument in $Arguments) {
        if ($argument -match '[\s"]') {
            '"' + ($argument -replace '"', '\"') + '"'
        }
        else {
            $argument
        }
    })

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

# Resolve-StartProcessArgs turns a command vector into a Start-Process FilePath +
# ArgumentList, wrapping .cmd/.bat (via cmd.exe) and .ps1 (via PowerShell) the way
# the OS requires when the shell isn't used.
function Resolve-StartProcessArgs {
    param([string]$Name, [string[]]$Arguments)

    $commandInfo = Get-Command $Name -ErrorAction Stop
    $commandPath = $commandInfo.Path
    if (-not $commandPath) { $commandPath = $commandInfo.Source }
    if (-not $commandPath) { $commandPath = $Name }

    $extension = [System.IO.Path]::GetExtension($commandPath).ToLowerInvariant()
    if ($extension -in @(".cmd", ".bat")) {
        $cmd = $env:ComSpec
        if (-not $cmd) { $cmd = "cmd.exe" }
        return [pscustomobject]@{ FilePath = $cmd; ArgumentList = @("/d", "/s", "/c", $commandPath) + $Arguments }
    }
    if ($extension -eq ".ps1") {
        $ps = (Get-Command "pwsh" -ErrorAction SilentlyContinue).Path
        if (-not $ps) { $ps = (Get-Command "powershell" -ErrorAction Stop).Path }
        return [pscustomobject]@{ FilePath = $ps; ArgumentList = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $commandPath) + $Arguments }
    }
    return [pscustomobject]@{ FilePath = $commandPath; ArgumentList = $Arguments }
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
    $resolved = Resolve-StartProcessArgs -Name $Command[0] -Arguments $arguments

    # Capture child output to per-service files rather than in-process redirected
    # pipes. The pipe + BeginOutputReadLine/Register-ObjectEvent approach
    # intermittently wedged Go children before they could bind under restart churn
    # (the child blocked on its first stdout write while the managed pipe wasn't
    # being drained). Files have no such backpressure path — verified to start
    # cleanly every time. Truncated on each (re)start; we tail them from offset 0.
    $logDir = Join-Path $Root ".logs"
    New-Item -ItemType Directory -Force -Path $logDir | Out-Null
    $outFile = Join-Path $logDir "$Name.out.log"
    $errFile = Join-Path $logDir "$Name.err.log"

    # The child inherits the parent environment; apply this service's vars first.
    # Starts are sequential, so later starts just overwrite earlier values.
    foreach ($key in $Environment.Keys) {
        Set-Item -Path ("Env:" + $key) -Value $Environment[$key]
    }

    $startParams = @{
        FilePath               = $resolved.FilePath
        WorkingDirectory       = $WorkingDirectory
        RedirectStandardOutput = $outFile
        RedirectStandardError  = $errFile
        WindowStyle            = "Hidden"
        PassThru               = $true
    }
    if ($resolved.ArgumentList.Count -gt 0) {
        $startParams.ArgumentList = $resolved.ArgumentList
    }
    $process = Start-Process @startParams

    [pscustomobject]@{
        Name             = $Name
        Process          = $process
        WorkingDirectory = $WorkingDirectory
        Environment      = $Environment
        Command          = $Command
        OutFile          = $outFile
        ErrFile          = $errFile
        OutOffset        = [long]0
        ErrOffset        = [long]0
        ExitReported     = $false
    }
}

function Receive-DevOutput {
    param([pscustomobject]$DevProcess)

    foreach ($spec in @(
            @{ File = $DevProcess.OutFile; Key = "OutOffset" },
            @{ File = $DevProcess.ErrFile; Key = "ErrOffset" }
        )) {
        $file = $spec.File
        if (-not $file -or -not (Test-Path -LiteralPath $file)) { continue }
        $fs = $null
        try {
            # Share ReadWrite so reading never blocks the child still writing.
            $fs = [System.IO.File]::Open($file, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
            $offset = [long]$DevProcess.($spec.Key)
            if ($fs.Length -lt $offset) { $offset = 0 } # file truncated on restart
            if ($fs.Length -gt $offset) {
                [void]$fs.Seek($offset, [System.IO.SeekOrigin]::Begin)
                $reader = New-Object System.IO.StreamReader($fs)
                $text = $reader.ReadToEnd()
                $DevProcess.($spec.Key) = $fs.Length
                $reader.Dispose()
                $fs = $null
                foreach ($line in ($text -split "`r?`n")) {
                    if ($line -ne "") { Write-Host "[$($DevProcess.Name)] $line" }
                }
            }
        }
        catch { }
        finally {
            if ($fs) { $fs.Dispose() }
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

# Liveness tuning for the health watchdog below. Services run as prebuilt
# binaries (see below), so a cold start is ~1-2s; the grace only needs to cover
# boot, not compilation — keeping hung-start recovery fast.
$StartupGraceSeconds = 20   # max time to become ready before a start is judged hung
$LivenessWindowSeconds = 20 # a previously-healthy service may blip this long before recycling
$ProbeIntervalSeconds = 3   # how often to poll each service's health endpoint

# New-HealthState is the per-service supervisor record. Healthy/UnhealthySince/
# LastProbe drive the liveness watchdog; Failures/RestartAt drive backoff.
function New-HealthState {
    @{
        Failures       = 0
        RestartAt      = $null
        StartedAt      = Get-Date
        Healthy        = $false
        UnhealthySince = $null
        LastProbe      = [datetime]::MinValue
    }
}

# Test-DevHealth probes a service's HTTP endpoint. Any response (even 4xx, e.g.
# the agent's 401 without a token) means the process is listening and handling
# requests; only a refused/timed-out connection counts as unhealthy. This is what
# lets the watchdog catch a child that started but hung before binding — something
# the exit-based restart can never see.
function Test-DevHealth {
    param([string]$Name)

    $url = $null
    $headers = @{}
    switch ($Name) {
        "api"   { $url = "http://${ApiConnectHost}:$ApiPort/api/v1/health" }
        "agent" { $url = "http://${ApiConnectHost}:$AgentPort/agent/v1/health"; $headers = @{ Authorization = "Bearer $AgentToken" } }
        "web"   { $url = "http://${ApiConnectHost}:$WebPort/" }
        default { return $true } # unknown service: don't supervise
    }

    try {
        $resp = Invoke-WebRequest -Uri $url -Headers $headers -UseBasicParsing -TimeoutSec 2 -ErrorAction Stop
        return ([int]$resp.StatusCode -lt 500)
    }
    catch {
        # An HTTP error status still proves the server is up and answering.
        if ($_.Exception.Response) {
            return $true
        }
        return $false
    }
}

# Build-GoService compiles a Go package to a fixed output path. `go build` writes
# the binary atomically (temp + rename), so on a compile error the previous
# working binary is left intact — the caller can keep running it. Returns whether
# the build succeeded.
function Build-GoService {
    param(
        [string]$Name,
        [string]$Dir,
        [string]$Package,
        [string]$Output
    )

    Write-Host "[$Name] building $Package..."
    Push-Location $Dir
    try {
        $buildOutput = & go build -o $Output $Package 2>&1
        $ok = ($LASTEXITCODE -eq 0)
        foreach ($line in $buildOutput) {
            if ($line) { Write-Host "[$Name] $line" }
        }
        return $ok
    }
    catch {
        Write-Host "[$Name] build error: $($_.Exception.Message)"
        return $false
    }
    finally {
        Pop-Location
    }
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
# Compile the Go services to stable paths up front and run the resulting
# binaries, rather than `go run` each launch. This makes restarts instant, avoids
# the `go run` build-cache wedge after a hard kill, and — because the executable
# path is now constant across runs — stops Windows Firewall from re-prompting to
# allow network access every launch (its rules are keyed by exe path, and
# `go run` produced a fresh temp path each time).
$BinDir = Join-Path $Root ".bin"
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
$ServerExe = Join-Path $BinDir "mcsm-api.exe"
$AgentExe = Join-Path $BinDir "mcsm-agent.exe"

if (-not (Build-GoService -Name "api" -Dir $ApiDir -Package "./cmd/server" -Output $ServerExe)) {
    throw "Failed to build the API server; fix the compile error above and re-run."
}
if (-not (Build-GoService -Name "agent" -Dir $AgentDir -Package "./cmd/agent" -Output $AgentExe)) {
    throw "Failed to build the agent; fix the compile error above and re-run."
}

$specs = [ordered]@{
    api   = @{ Dir = $ApiDir;   Env = $ApiEnv;   Command = @($ServerExe) }
    agent = @{ Dir = $AgentDir; Env = $AgentEnv; Command = @($AgentExe) }
    web   = @{ Dir = $WebDir;   Env = $WebEnv;   Command = @("pnpm", "dev", "--host", $BindHost, "--port", "$WebPort") }
}
$running = @{}
$health = @{}

function Start-Spec {
    param([string]$Name)

    $running[$Name] = Start-DevProcess -Name $Name -WorkingDirectory $specs[$Name].Dir -Environment $specs[$Name].Env -Command $specs[$Name].Command
    $health[$Name] = New-HealthState
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
                $state.Healthy = $false
                $state.UnhealthySince = $null
                $state.LastProbe = Get-Date
                $running[$name] = Restart-DevProcess -DevProcess $running[$name] -Reason "recovering"
            }
        }

        # Liveness watchdog: a service can start but hang before it ever serves a
        # request (observed with `go run` children that wedge before binding).
        # The exit-based restart above never fires for those, so we also probe
        # each service's health endpoint and recycle one that never becomes ready
        # within its startup grace, or that stops responding while still running.
        foreach ($name in @($running.Keys)) {
            $state = $health[$name]
            $devProcess = $running[$name]
            if ($state.RestartAt -or $devProcess.Process.HasExited) {
                continue
            }
            if (((Get-Date) - $state.LastProbe).TotalSeconds -lt $ProbeIntervalSeconds) {
                continue
            }
            $state.LastProbe = Get-Date

            if (Test-DevHealth -Name $name) {
                if (-not $state.Healthy) {
                    $state.Healthy = $true
                    Write-Host "[$name] healthy"
                }
                $state.UnhealthySince = $null
                continue
            }

            if (-not $state.Healthy) {
                # Never became ready. The grace covers `go run` compile + boot.
                if (((Get-Date) - $state.StartedAt).TotalSeconds -ge $StartupGraceSeconds) {
                    $state.Failures++
                    $delay = Get-RestartDelaySeconds -Failures $state.Failures
                    $state.RestartAt = (Get-Date).AddSeconds($delay)
                    Write-Host "[$name] no response ${StartupGraceSeconds}s after start (hung); restarting in ${delay}s"
                }
            }
            else {
                # Was healthy, then stopped responding while still alive.
                if (-not $state.UnhealthySince) {
                    $state.UnhealthySince = Get-Date
                }
                if (((Get-Date) - $state.UnhealthySince).TotalSeconds -ge $LivenessWindowSeconds) {
                    $state.Failures++
                    $delay = Get-RestartDelaySeconds -Failures $state.Failures
                    $state.RestartAt = (Get-Date).AddSeconds($delay)
                    $state.Healthy = $false
                    $state.UnhealthySince = $null
                    Write-Host "[$name] unresponsive ${LivenessWindowSeconds}s; restarting in ${delay}s"
                }
            }
        }

        if (-not $NoBackendWatch -and (Get-Date) -ge $watchReadyAt) {
            $now = Get-Date
            if (($now - $lastWatchCheck).TotalMilliseconds -ge 1000) {
                $lastWatchCheck = $now

                $nextApiFingerprint = Get-DevSourceFingerprint -Path $ApiDir -Extensions $apiExtensions -FileNames $apiFileNames
                if ($nextApiFingerprint -ne $apiFingerprint) {
                    $apiFingerprint = Get-StableDevSourceFingerprint -Path $ApiDir -Extensions $apiExtensions -FileNames $apiFileNames
                    # Stop first so the running exe is unlocked and can be replaced.
                    Write-Host "[api] source changed; rebuilding..."
                    Stop-DevProcess -DevProcess $running["api"]
                    if (-not (Build-GoService -Name "api" -Dir $ApiDir -Package "./cmd/server" -Output $ServerExe)) {
                        Write-Host "[api] build failed; restarting the previous build"
                    }
                    Start-Spec -Name "api"
                }

                $nextAgentFingerprint = Get-DevSourceFingerprint -Path $AgentDir -Extensions $agentExtensions -FileNames $agentFileNames
                if ($nextAgentFingerprint -ne $agentFingerprint) {
                    $agentFingerprint = Get-StableDevSourceFingerprint -Path $AgentDir -Extensions $agentExtensions -FileNames $agentFileNames
                    Write-Host "[agent] source changed; rebuilding..."
                    Stop-DevProcess -DevProcess $running["agent"]
                    if (-not (Build-GoService -Name "agent" -Dir $AgentDir -Package "./cmd/agent" -Output $AgentExe)) {
                        Write-Host "[agent] build failed; restarting the previous build"
                    }
                    Start-Spec -Name "agent"
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
                    $health["web"] = New-HealthState
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
