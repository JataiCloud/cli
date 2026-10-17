# scripts/install.ps1 — instala a CLI do Jataí no Windows.
# Uso: irm https://jataicloud.com.br/install.ps1 | iex
$ErrorActionPreference = 'Stop'
# A barra de progresso do Invoke-WebRequest deixa o download muito mais lento.
$ProgressPreference = 'SilentlyContinue'
$Repo = 'jataicloud/cli'
$InstallDir = if ($env:JATAI_INSTALL_DIR) { $env:JATAI_INSTALL_DIR } else { Join-Path $HOME '.jatai\bin' }

if ($env:JATAI_VERSION) {
  $Version = $env:JATAI_VERSION.TrimStart('v')
} else {
  $Version = (Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest").tag_name.TrimStart('v')
}

$Archive = "jatai_${Version}_windows_amd64.zip"
$Base = "https://github.com/$Repo/releases/download/v$Version"
$Tmp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid())
New-Item -ItemType Directory -Path $Tmp | Out-Null
try {
  Write-Host "Baixando jatai $Version (windows/amd64)..."
  Invoke-WebRequest "$Base/$Archive" -OutFile (Join-Path $Tmp $Archive)
  Invoke-WebRequest "$Base/checksums.txt" -OutFile (Join-Path $Tmp 'checksums.txt')
  $Line = Select-String -Path (Join-Path $Tmp 'checksums.txt') -Pattern " $([regex]::Escape($Archive))$"
  if (-not $Line) { throw "Checksum de $Archive não encontrado" }
  $Expected = $Line.Line.Split(' ')[0]
  $Actual = (Get-FileHash (Join-Path $Tmp $Archive) -Algorithm SHA256).Hash.ToLower()
  if ($Expected -ne $Actual) { throw 'Checksum não confere — download corrompido ou adulterado' }
  Expand-Archive (Join-Path $Tmp $Archive) -DestinationPath $Tmp -Force
  New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
  Move-Item (Join-Path $Tmp 'jatai.exe') (Join-Path $InstallDir 'jatai.exe') -Force
} finally {
  Remove-Item $Tmp -Recurse -Force
}

$UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($UserPath -split ';') -contains $InstallDir)) {
  [Environment]::SetEnvironmentVariable('Path', "$UserPath;$InstallDir", 'User')
  Write-Host 'Adicionado ao PATH do usuário. Abra um novo terminal.'
}
Write-Host "jatai $Version instalada em $InstallDir. Próximo passo: jatai login"
