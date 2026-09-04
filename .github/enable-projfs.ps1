# enable-projfs.ps1 - Enable Windows Projected File System feature.
# Windows Server uses Install-WindowsFeature (FS-Projectedfs);
# Windows 10/11 desktop uses Enable-WindowsOptionalFeature (Client-ProjFS).

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$serverFeature = Get-WindowsFeature -Name FS-Projectedfs -ErrorAction SilentlyContinue
if ($serverFeature) {
    Write-Host "Windows Server detected - FS-Projectedfs state: $($serverFeature.InstallState)"
    if ($serverFeature.InstallState -ne 'Installed') {
        Install-WindowsFeature -Name FS-Projectedfs
        Write-Host "ProjFS installed via Install-WindowsFeature"
    }
    return
}

$clientFeature = Get-WindowsOptionalFeature -Online -FeatureName Client-ProjFS -ErrorAction SilentlyContinue
if ($clientFeature) {
    Write-Host "Windows desktop detected - Client-ProjFS state: $($clientFeature.State)"
    if ($clientFeature.State -ne 'Enabled') {
        Enable-WindowsOptionalFeature -Online -FeatureName Client-ProjFS -NoRestart
        Write-Host "ProjFS enabled via Enable-WindowsOptionalFeature"
    }
    return
}

Write-Host "::warning::ProjFS feature not found on this OS - ProjFS tests will be skipped"
