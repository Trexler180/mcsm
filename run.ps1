param(
    [int]$ApiPort = 8080,
    [int]$AgentPort = 8090,
    [int]$WebPort = 3000,
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

function Require-Command {
    param([string]$Name)
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Required command '$Name' was not found on PATH."
    }
}

function Start-DevJob {
    param(
        [string]$Name,
        [string]$WorkingDirectory,
        [hashtable]$Environment,
        [string[]]$Command
    )

    Start-Job -Name $Name -ArgumentList $WorkingDirectory, $Environment, $Command -ScriptBlock {
        param($WorkingDirectory, $Environment, $Command)

        Set-Location $WorkingDirectory
        foreach ($key in $Environment.Keys) {
            Set-Item -Path "env:$key" -Value $Environment[$key]
        }

        $exe = $Command[0]
        $args = @()
        if ($Command.Count -gt 1) {
            $args = $Command[1..($Command.Count - 1)]
        }

        & $exe @args
        exit $LASTEXITCODE
    }
}

function Stop-DevJobs {
    param([System.Management.Automation.Job[]]$Jobs)
    foreach ($job in $Jobs) {
        if ($job.State -eq "Running") {
            Stop-Job $job -ErrorAction SilentlyContinue
        }
        Remove-Job $job -Force -ErrorAction SilentlyContinue
    }
}

Require-Command "go"
Require-Command "pnpm"

New-Item -ItemType Directory -Force -Path $ServerRoot | Out-Null

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
    API_HOST = "127.0.0.1"
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
    AGENT_HOST = "127.0.0.1"
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
Write-Host "  Web:   http://localhost:$WebPort"
Write-Host "  API:   http://localhost:$ApiPort"
Write-Host "  Agent: http://localhost:$AgentPort"
Write-Host "  Admin: $AdminEmail / $AdminPassword"
Write-Host ""
Write-Host "Press Ctrl+C to stop all services."
Write-Host ""

$jobs = @(
    Start-DevJob -Name "api" -WorkingDirectory $ApiDir -Environment $ApiEnv -Command @("go", "run", "./cmd/server")
    Start-DevJob -Name "agent" -WorkingDirectory $AgentDir -Environment $AgentEnv -Command @("go", "run", "./cmd/agent")
    Start-DevJob -Name "web" -WorkingDirectory $WebDir -Environment $WebEnv -Command @("pnpm", "dev", "--host", "127.0.0.1", "--port", "$WebPort")
)

try {
    while ($true) {
        foreach ($job in $jobs) {
            $jobErrors = @()
            Receive-Job $job -ErrorVariable jobErrors -ErrorAction SilentlyContinue | ForEach-Object {
                Write-Host "[$($job.Name)] $_"
            }
            foreach ($err in $jobErrors) {
                Write-Host "[$($job.Name)] $err"
            }
        }

        $failed = $jobs | Where-Object { $_.State -in @("Failed", "Stopped", "Completed") }
        if ($failed.Count -gt 0) {
            foreach ($job in $failed) {
                $jobErrors = @()
                Receive-Job $job -ErrorVariable jobErrors -ErrorAction SilentlyContinue | ForEach-Object {
                    Write-Host "[$($job.Name)] $_"
                }
                foreach ($err in $jobErrors) {
                    Write-Host "[$($job.Name)] $err"
                }
                Write-Host "[$($job.Name)] exited with state $($job.State)"
            }
            break
        }

        Start-Sleep -Milliseconds 250
    }
}
finally {
    Write-Host ""
    Write-Host "Stopping ServerManager..."
    Stop-DevJobs -Jobs $jobs
}
