param([string]$OutputPath)
& (Join-Path $PSScriptRoot 'scripts/build.ps1') @PSBoundParameters
