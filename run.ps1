param(
    [int]$ApiPort = 8080,
    [int]$AgentPort = 8090,
    [int]$WebPort = 3000,
    [string]$BindHost = "0.0.0.0",
    [string]$PublicHost = "",
    [string]$AdminEmail = "admin@example.com",
    [string]$AdminPassword = "changeme",
    [string]$JwtSecret = "local-dev-jwt-secret-change-me",
    [string]$AgentToken = "dev-agent-token",
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
        OutputEventId = $outputEventId
        ErrorEventId = $errorEventId
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

function Stop-DevProcesses {
    param([object[]]$Processes)

    if (-not $Processes) {
        return
    }

    foreach ($devProcess in $Processes) {
        if (-not $devProcess) {
            continue
        }

        Receive-DevOutput -DevProcess $devProcess

        if (-not $devProcess.Process.HasExited) {
            Stop-ProcessTree -ProcessId $devProcess.Process.Id
            [void]$devProcess.Process.WaitForExit(1000)
        }

        Receive-DevOutput -DevProcess $devProcess
        Unregister-Event -SourceIdentifier $devProcess.OutputEventId -ErrorAction SilentlyContinue
        Unregister-Event -SourceIdentifier $devProcess.ErrorEventId -ErrorAction SilentlyContinue
    }
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

if (-not $SkipInstall) {
    Write-Host "[setup] Installing web dependencies..."
    Push-Location $WebDir
    try {
        pnpm install
    }
    finally {
        Pop-Location
    }
}

$ApiEnv = @{
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
    AGENT_HOST = $BindHost
    AGENT_PORT = "$AgentPort"
    AGENT_TOKEN = $AgentToken
    AGENT_SERVER_ROOT = $ServerRoot
}

$WebEnv = @{
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
Write-Host ""
Write-Host "Press Ctrl+C to stop all services."
Write-Host ""

$processes = @()
try {
    $processes += Start-DevProcess -Name "api" -WorkingDirectory $ApiDir -Environment $ApiEnv -Command @("go", "run", "./cmd/server")
    $processes += Start-DevProcess -Name "agent" -WorkingDirectory $AgentDir -Environment $AgentEnv -Command @("go", "run", "./cmd/agent")
    $processes += Start-DevProcess -Name "web" -WorkingDirectory $WebDir -Environment $WebEnv -Command @("pnpm", "dev", "--host", $BindHost, "--port", "$WebPort")

    while ($true) {
        if ($global:ServerManagerStopRequested -or $global:ServerManagerForceStopRequested) {
            break
        }

        foreach ($devProcess in $processes) {
            Receive-DevOutput -DevProcess $devProcess
        }

        $exited = $processes | Where-Object { $_.Process.HasExited }
        if ($exited.Count -gt 0) {
            foreach ($devProcess in $exited) {
                Receive-DevOutput -DevProcess $devProcess
                Write-Host "[$($devProcess.Name)] exited with code $($devProcess.Process.ExitCode)"
            }
            break
        }

        Start-Sleep -Milliseconds 250
    }
}
finally {
    Write-Host ""
    Write-Host "Stopping ServerManager..."
    Stop-DevProcesses -Processes $processes
    Unregister-Event -SourceIdentifier $cancelEventId -ErrorAction SilentlyContinue
    Remove-Job -Job $cancelSubscriber -Force -ErrorAction SilentlyContinue
}
