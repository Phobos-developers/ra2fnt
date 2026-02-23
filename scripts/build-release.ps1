param(
  [string]$Version = ""
)

$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = (Resolve-Path (Join-Path $ScriptDir "..")).Path

if ([string]::IsNullOrWhiteSpace($Version)) {
  try {
    $sha = (git -C $RepoRoot rev-parse --short HEAD).Trim()
    if ([string]::IsNullOrWhiteSpace($sha)) {
      $Version = "dev"
    } else {
      $Version = "dev-$sha"
    }
  } catch {
    $Version = "dev"
  }
}

$OutDir = Join-Path $RepoRoot "dist"
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

$Targets = @(
  @{ GOOS = "linux"; GOARCH = "amd64" },
  @{ GOOS = "linux"; GOARCH = "arm64" },
  @{ GOOS = "darwin"; GOARCH = "amd64" },
  @{ GOOS = "darwin"; GOARCH = "arm64" },
  @{ GOOS = "windows"; GOARCH = "amd64" },
  @{ GOOS = "windows"; GOARCH = "arm64" }
)

Push-Location $RepoRoot
try {
  foreach ($target in $Targets) {
    $ext = if ($target.GOOS -eq "windows") { ".exe" } else { "" }
    $outFile = Join-Path $OutDir ("ra2fnt-{0}-{1}{2}" -f $target.GOOS, $target.GOARCH, $ext)

    Write-Host "building $outFile"
    $env:GOOS = $target.GOOS
    $env:GOARCH = $target.GOARCH
    $env:CGO_ENABLED = "0"

    go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $outFile ./src/cmd/ra2fnt
  }
} finally {
  Remove-Item Env:GOOS -ErrorAction SilentlyContinue
  Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
  Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
  Pop-Location
}

Write-Host "done: version=$Version"
Write-Host "artifacts: $OutDir"
