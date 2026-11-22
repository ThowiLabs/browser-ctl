# browserctl.ps1 — thin wrapper around the browser-ctl binary for agent hosts.
#
# Usage:
#   browserctl.ps1 run "goto https://example.com`nget title" [-Json] [-Port N]
#   browserctl.ps1 serve [-Port N] [-NoWait]
#   browserctl.ps1 tabs [-Port N]
#   browserctl.ps1 status
#
# With -Json, the result is printed as a single JSON object:
#   {"ok":true,"output":"..."}   or   {"ok":false,"error":"..."}

param(
    [Parameter(Position = 0)][string]$Command,
    [Parameter(Position = 1, ValueFromRemainingArguments = $true)][string[]]$Commands,
    [switch]$Json,
    [int]$Port = 0,
    [switch]$NoWait
)

$RepoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$Bin = Join-Path $RepoRoot "bin/browser-ctl.exe"
if (-not (Test-Path $Bin)) {
    $Bin = Join-Path $RepoRoot "bin/browser-ctl"
}
if (-not (Test-Path $Bin)) {
    if ($Json) {
        '{"ok":false,"error":"browser-ctl binary not found; run: CGO_ENABLED=0 go build -trimpath -ldflags \"-s -w\" -o bin/browser-ctl ./cmd/browser-ctl"}'
    } else {
        Write-Error "browser-ctl binary not found at $Bin"
    }
    exit 1
}

$args = @()
if ($Command -ne "") { $args += $Command }
$args += $Commands

switch ($Command) {
    "run"   { $sub = "run" }
    "serve" { $sub = "serve" }
    "tabs"  { $sub = "tabs" }
    "status" { $sub = "status" }
    default {
        if ($Json) { '{"ok":false,"error":"first argument must be run|serve|tabs|status"}' }
        else { Write-Error "first argument must be run|serve|tabs|status" }
        exit 2
    }
}

# For run, the commands are the remaining arguments joined by newlines.
$finalArgs = @($sub)
if ($Port -ne 0) { $finalArgs += @("--port", [string]$Port) }
if ($sub -eq "serve" -and $NoWait) { $finalArgs += "--no-wait" }
if ($sub -eq "run") {
    $cmdText = ($Commands -join "`n")
    if ($cmdText -ne "") { $finalArgs += $cmdText }
}

$output = & $Bin @finalArgs 2>&1 | Out-String
$code = $LASTEXITCODE

if ($Json) {
    $obj = if ($code -eq 0) {
        [pscustomobject]@{ ok = $true; output = $output.Trim() }
    } else {
        [pscustomobject]@{ ok = $false; error = $output.Trim() }
    }
    $obj | ConvertTo-Json -Compress
} else {
    Write-Output $output
}
exit $code
