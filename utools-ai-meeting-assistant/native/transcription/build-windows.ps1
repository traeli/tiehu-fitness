param(
  [Parameter(Mandatory = $true)]
  [string]$WhisperSource,
  [ValidateSet("x64", "arm64")]
  [string]$Architecture = "x64"
)

$ErrorActionPreference = "Stop"
$ScriptDirectory = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectDirectory = Resolve-Path (Join-Path $ScriptDirectory "..\..")
$BuildDirectory = Join-Path $ProjectDirectory ".native-build\whisper-$Architecture"
$OutputDirectory = Join-Path $ProjectDirectory "plugin\native\win32-$Architecture"

if (-not (Get-Command cmake.exe -ErrorAction SilentlyContinue)) {
  throw "cmake.exe was not found"
}

$GeneratorArchitecture = if ($Architecture -eq "arm64") { "ARM64" } else { "x64" }
cmake.exe -S $WhisperSource -B $BuildDirectory -A $GeneratorArchitecture `
  -DWHISPER_BUILD_EXAMPLES=OFF -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_SERVER=OFF `
  -DBUILD_SHARED_LIBS=OFF
if ($LASTEXITCODE -ne 0) { throw "Whisper CMake configuration failed" }
cmake.exe --build $BuildDirectory --config Release --target whisper
if ($LASTEXITCODE -ne 0) { throw "Whisper build failed" }

New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$Includes = @(
  (Join-Path $WhisperSource "include"),
  (Join-Path $WhisperSource "ggml\include")
)
$Libraries = @(
  (Join-Path $BuildDirectory "src\Release\whisper.lib"),
  (Join-Path $BuildDirectory "ggml\src\Release\ggml.lib"),
  (Join-Path $BuildDirectory "ggml\src\Release\ggml-base.lib"),
  (Join-Path $BuildDirectory "ggml\src\Release\ggml-cpu.lib")
)
$IncludeFlags = $Includes | ForEach-Object { "/I$_" }

cl.exe /nologo /std:c++17 /O2 /EHsc /W4 /utf-8 /MT $IncludeFlags `
  (Join-Path $ScriptDirectory "main.cpp") `
  /Fe:(Join-Path $OutputDirectory "tiehu-local-whisper.exe") /link $Libraries
if ($LASTEXITCODE -ne 0) { throw "Local Whisper helper build failed" }
