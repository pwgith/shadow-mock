# Runs the same checks as scripts/lint.sh on Windows, where there is no bash.
#
# Usage: scripts/lint.ps1 [-Fix]
param([switch]$Fix)

$ErrorActionPreference = 'Continue'
$GolangciVersion = if ($env:GOLANGCI_LINT_VERSION) { $env:GOLANGCI_LINT_VERSION } else { 'v2.14.0' }
$GovulncheckVersion = if ($env:GOVULNCHECK_VERSION) { $env:GOVULNCHECK_VERSION } else { 'v1.9.0' }

Set-Location (Join-Path $PSScriptRoot '..')
if (-not (Test-Path go.mod)) {
    Write-Error 'go.mod not found; run from the repository root.'
    exit 2
}

$script:failed = $false
function Section($name) { Write-Host "`n== $name ==" }
function Fail($msg) { Write-Host "FAIL: $msg"; $script:failed = $true }

function Golangci {
    go run "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$GolangciVersion" @args
    return $LASTEXITCODE -eq 0
}

$files = Get-ChildItem -Recurse -File -Filter *.go |
    Where-Object { $_.FullName -notmatch '[\\/](vendor|\.git)[\\/]' -and $_.Name -notmatch '(\.pb|_gen)\.go$' }

Section 'file names'
$bad = $files | Where-Object { $_.Name -cnotmatch '^[a-z0-9]+(_[a-z0-9]+)*\.go$' }
if ($bad) { Fail 'file names must be lower_snake_case.go:'; $bad | ForEach-Object { $_.FullName } }

Section 'package names'
$bad = $files | Select-String -Pattern '^package (util|utils|common|helpers|helper|misc|base|shared)$'
if ($bad) { Fail 'package names util/common/helpers/misc/base/shared are not allowed:'; $bad | ForEach-Object { $_.Path } }

Section 'TODO owners'
$bad = $files | Select-String -Pattern '//\s*(TODO|FIXME)([^(]|$)'
if ($bad) { Fail 'TODO/FIXME must be written as TODO(owner): or TODO(#issue):'; $bad | ForEach-Object { "$($_.Path):$($_.LineNumber): $($_.Line.Trim())" } }

Section 'golangci-lint config'
if (-not (Golangci config verify)) { Fail 'invalid .golangci.yml' }

Section 'golangci-lint'
if ($Fix) {
    if (-not (Golangci fmt ./...)) { Fail 'golangci-lint fmt' }
    if (-not (Golangci run --fix ./...)) { Fail 'golangci-lint findings' }
} else {
    if (-not (Golangci fmt --diff ./...)) { Fail 'code is not formatted; run make fmt' }
    if (-not (Golangci run ./...)) { Fail 'golangci-lint findings' }
}

Section 'go mod tidy'
go mod tidy -diff
if ($LASTEXITCODE -ne 0) { Fail 'go.mod/go.sum are not tidy; run go mod tidy' }

Section 'govulncheck'
go run "golang.org/x/vuln/cmd/govulncheck@$GovulncheckVersion" ./...
if ($LASTEXITCODE -ne 0) { Fail 'govulncheck found vulnerabilities' }

Write-Host ''
if ($script:failed) {
    Write-Host 'Lint FAILED. See docs/CODING_STANDARDS.md.'
    exit 1
}
Write-Host 'Lint passed.'
