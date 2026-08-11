param(
    [Parameter(Mandatory = $true)]
    [string]$Source,

    [Parameter(Mandatory = $true)]
    [string]$Destination
)

$ErrorActionPreference = 'Stop'

$sourcePath = (Resolve-Path -LiteralPath $Source).Path
$destinationPath = [System.IO.Path]::GetFullPath($Destination)
$destinationBinary = Join-Path $destinationPath 'secretvault.exe'

New-Item -ItemType Directory -Force -Path $destinationPath | Out-Null
Copy-Item -LiteralPath $sourcePath -Destination $destinationBinary -Force

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$pathEntries = @($userPath -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
$alreadyPresent = $pathEntries | Where-Object {
    $_.TrimEnd('\') -ieq $destinationPath.TrimEnd('\')
}

if (-not $alreadyPresent) {
    $updatedPath = if ([string]::IsNullOrWhiteSpace($userPath)) {
        $destinationPath
    } else {
        $userPath.TrimEnd(';') + ';' + $destinationPath
    }
    [Environment]::SetEnvironmentVariable('Path', $updatedPath, 'User')
    Write-Host "Added $destinationPath to the user PATH. Open a new terminal to use it."
}

Write-Host "Installed $destinationBinary"
