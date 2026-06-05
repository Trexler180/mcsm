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
    param([pscustomobject]$DevProcess)

    Write-Host "[$($DevProcess.Name)] source changed; restarting..."
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

$processes = @()
$apiProcess = $null
$agentProcess = $null
$webProcess = $null
try {
    $apiProcess = Start-DevProcess -Name "api" -WorkingDirectory $ApiDir -Environment $ApiEnv -Command @("go", "run", "./cmd/server")
    $agentProcess = Start-DevProcess -Name "agent" -WorkingDirectory $AgentDir -Environment $AgentEnv -Command @("go", "run", "./cmd/agent")
    $processes = @($apiProcess, $agentProcess)
    Wait-DevHttp -Name "api" -Url "http://${ApiConnectHost}:$ApiPort/api/v1/health" -Processes $processes

    # The initial boot resets the dev admin password for convenience. Hot
    # backend reloads must not repeat that reset, because it deletes refresh
    # tokens and forces browser sessions to log in again.
    $ApiEnv["RESET_ADMIN_PASSWORD"] = "0"

    $apiExtensions = @(".go", ".sql")
    $apiFileNames = @("go.mod", "go.sum")
    $agentExtensions = @(".go")
    $agentFileNames = @("go.mod", "go.sum")
    $apiFingerprint = Get-StableDevSourceFingerprint -Path $ApiDir -Extensions $apiExtensions -FileNames $apiFileNames
    $agentFingerprint = Get-StableDevSourceFingerprint -Path $AgentDir -Extensions $agentExtensions -FileNames $agentFileNames
    $lastWatchCheck = Get-Date
    $watchReadyAt = (Get-Date).AddSeconds(5)

    $webProcess = Start-DevProcess -Name "web" -WorkingDirectory $WebDir -Environment $WebEnv -Command @("pnpm", "dev", "--host", $BindHost, "--port", "$WebPort")
    $processes = @($apiProcess, $agentProcess, $webProcess)

    while ($true) {
        if ($global:ServerManagerStopRequested -or $global:ServerManagerForceStopRequested) {
            break
        }

        $processes = @($apiProcess, $agentProcess, $webProcess) | Where-Object { $_ }
        foreach ($devProcess in $processes) {
            Receive-DevOutput -DevProcess $devProcess
        }

        $exited = @($processes | Where-Object { $_.Process.HasExited })
        foreach ($devProcess in $exited) {
            if (-not $devProcess.ExitReported) {
                Receive-DevOutput -DevProcess $devProcess
                Write-Host "[$($devProcess.Name)] exited with code $($devProcess.Process.ExitCode)"
                $devProcess.ExitReported = $true
            }
        }

        if (-not $NoBackendWatch -and (Get-Date) -ge $watchReadyAt) {
            $now = Get-Date
            if (($now - $lastWatchCheck).TotalMilliseconds -ge 1000) {
                $lastWatchCheck = $now

                $nextApiFingerprint = Get-DevSourceFingerprint -Path $ApiDir -Extensions $apiExtensions -FileNames $apiFileNames
                if ($nextApiFingerprint -ne $apiFingerprint) {
                    $apiFingerprint = Get-StableDevSourceFingerprint -Path $ApiDir -Extensions $apiExtensions -FileNames $apiFileNames
                    $apiProcess = Restart-DevProcess -DevProcess $apiProcess
                    Wait-DevHttp -Name "api" -Url "http://${ApiConnectHost}:$ApiPort/api/v1/health" -Processes @($apiProcess) -AllowExited
                }

                $nextAgentFingerprint = Get-DevSourceFingerprint -Path $AgentDir -Extensions $agentExtensions -FileNames $agentFileNames
                if ($nextAgentFingerprint -ne $agentFingerprint) {
                    $agentFingerprint = Get-StableDevSourceFingerprint -Path $AgentDir -Extensions $agentExtensions -FileNames $agentFileNames
                    $agentProcess = Restart-DevProcess -DevProcess $agentProcess
                }
            }
        }

        $processes = @($apiProcess, $agentProcess, $webProcess) | Where-Object { $_ }
        $exited = @($processes | Where-Object { $_.Process.HasExited })
        if ($NoBackendWatch) {
            $blockingExited = $exited
        }
        else {
            $blockingExited = @($exited | Where-Object { $_.Name -notin @("api", "agent") })
        }

        if ($blockingExited.Count -gt 0) {
            break
        }

        Start-Sleep -Milliseconds 250
    }
}
finally {
    Write-Host ""
    Write-Host "Stopping ServerManager..."
    Stop-DevProcesses -Processes @($apiProcess, $agentProcess, $webProcess)
    Unregister-Event -SourceIdentifier $cancelEventId -ErrorAction SilentlyContinue
    Remove-Job -Job $cancelSubscriber -Force -ErrorAction SilentlyContinue
}
