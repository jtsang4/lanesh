# Lanesh installer for Windows.
#
#   irm {{.ServerURL}}/install.ps1 | iex
#   & ([scriptblock]::Create((irm {{.ServerURL}}/install.ps1))) join <lane-url> --key <join-key>
#
# Arguments are passed to lanesh once it is installed.
# Set LANESH_NO_MODIFY_PATH=1 to skip adding the bin directory to your user PATH.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Version = if ($env:LANESH_VERSION) { $env:LANESH_VERSION } else { '{{.Version}}' }
$Base = if ($env:LANESH_DOWNLOAD_BASE) { $env:LANESH_DOWNLOAD_BASE } else { 'https://github.com/jtsang4/lanesh/releases/download' }
$DefaultHome = Join-Path $env:USERPROFILE '.lanesh'
$LaneshDir = if ($env:LANESH_HOME) { $env:LANESH_HOME } else { $DefaultHome }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  default { throw "lanesh install: unsupported architecture $env:PROCESSOR_ARCHITECTURE" }
}

if ($Version -eq 'latest') {
  $Version = (Invoke-RestMethod -UseBasicParsing 'https://api.github.com/repos/jtsang4/lanesh/releases/latest').tag_name.TrimStart('v')
}

$asset = "lanesh_${Version}_windows_${arch}.exe"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("lanesh-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "Downloading lanesh v$Version (windows/$arch)..."
  Invoke-WebRequest -UseBasicParsing "$Base/v$Version/$asset" -OutFile (Join-Path $tmp 'lanesh.exe')
  Invoke-WebRequest -UseBasicParsing "$Base/v$Version/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

  $expected = (Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { ($_ -split '\s+')[1] -eq $asset } | ForEach-Object { ($_ -split '\s+')[0] })
  if (-not $expected) { throw "lanesh install: no checksum for $asset" }
  $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp 'lanesh.exe')).Hash.ToLower()
  if ($actual -ne $expected.ToLower()) { throw "lanesh install: checksum mismatch for $asset" }

  $binDir = Join-Path $LaneshDir 'bin'
  New-Item -ItemType Directory -Force -Path $binDir | Out-Null
  $exe = Join-Path $binDir 'lanesh.exe'
  if (Test-Path $exe) {
    # A running executable cannot be replaced or deleted, but it can be renamed.
    Get-ChildItem -Path $binDir -Filter 'lanesh.exe.old*' -ErrorAction SilentlyContinue | Remove-Item -Force -ErrorAction SilentlyContinue
    Rename-Item -Path $exe -NewName ("lanesh.exe.old-" + [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds())
  }
  Move-Item -Path (Join-Path $tmp 'lanesh.exe') -Destination $exe
  Write-Host "Installed $exe"

  $customHome = $LaneshDir.TrimEnd('\', '/') -ne $DefaultHome.TrimEnd('\', '/')
  $noModify = $env:LANESH_NO_MODIFY_PATH -and $env:LANESH_NO_MODIFY_PATH -notin @('0', 'false', 'FALSE')
  if (-not $customHome -and -not $noModify) {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = if ($userPath) { $userPath -split ';' } else { @() }
    $hasDir = $entries | Where-Object { $_.Trim().TrimEnd('\') -eq $binDir.TrimEnd('\') }
    if (-not $hasDir) {
      $newUserPath = if ($userPath) { "$binDir;$userPath" } else { $binDir }
      [Environment]::SetEnvironmentVariable('Path', $newUserPath, 'User')
      $env:Path = "$binDir;$env:Path"
      Write-Host "Added $binDir to your user PATH (open a new terminal to pick it up)."
    } elseif (-not (($env:Path -split ';') | Where-Object { $_.Trim().TrimEnd('\') -eq $binDir.TrimEnd('\') })) {
      $env:Path = "$binDir;$env:Path"
    }
  } elseif ($args.Count -eq 0 -and -not (($env:Path -split ';') | Where-Object { $_.Trim().TrimEnd('\') -eq $binDir.TrimEnd('\') })) {
    Write-Host "Add $binDir to your PATH to use lanesh."
  }
} finally {
  Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmp
}

if ($args.Count -gt 0) {
  & $exe @args
  exit $LASTEXITCODE
}
