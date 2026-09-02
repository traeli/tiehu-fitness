param(
  [ValidateSet("x64", "arm64")]
  [string]$Architecture = "x64"
)

$ErrorActionPreference = "Stop"
$ScriptDirectory = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectDirectory = Resolve-Path (Join-Path $ScriptDirectory "..\..")
$OutputDirectory = Join-Path $ProjectDirectory "plugin\native\win32-$Architecture"

if (-not (Get-Command cl.exe -ErrorAction SilentlyContinue)) {
  throw "cl.exe was not found. Run this script in a Visual Studio Developer PowerShell."
}
if ($env:VSCMD_ARG_TGT_ARCH -and $env:VSCMD_ARG_TGT_ARCH -ne $Architecture) {
  throw "Visual Studio target architecture is $env:VSCMD_ARG_TGT_ARCH, expected $Architecture."
}

New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$OutputFile = Join-Path $OutputDirectory "tiehu-system-audio.exe"

& cl.exe /nologo /std:c++17 /O2 /EHsc /W4 /utf-8 /MT /DUNICODE /D_UNICODE `
  (Join-Path $ScriptDirectory "main.cpp") `
  /Fe:$OutputFile /link ole32.lib

if ($LASTEXITCODE -ne 0) {
  throw "Windows native audio build failed with exit code $LASTEXITCODE."
}
