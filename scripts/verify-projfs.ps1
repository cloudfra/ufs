# scripts/verify-projfs.ps1 - Verify that Windows Projected File System is usable.
# Fails unless the feature is installed, projectedfslib.dll is present and the
# PrjFlt filter driver is loaded. Run scripts/enable-projfs.ps1 first.
# Reading the feature state and the loaded filters needs administrator privileges.

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$failures = @()

$os = Get-CimInstance -ClassName Win32_OperatingSystem
Write-Host "OS: $($os.Caption) $($os.Version) ($($os.OSArchitecture)), 64-bit process: $([Environment]::Is64BitProcess)"

# 1. The Windows optional feature is enabled. Microsoft documents it as
# Client-ProjFS; other sources name it Projected-FileSystem on Windows Server.
$featureFound = $false
foreach ($name in 'Client-ProjFS', 'Projected-FileSystem') {
    $feature = $null
    try {
        $feature = Get-WindowsOptionalFeature -Online -FeatureName $name
    } catch {
        Write-Host "Get-WindowsOptionalFeature $name failed: $($_.Exception.Message)"
    }
    if ($feature) {
        $featureFound = $true
        Write-Host "$name state: $($feature.State)"
        if ($feature.State -ne 'Enabled') {
            $failures += "Windows optional feature $name is $($feature.State), want Enabled"
        }
        break
    }
}
if (-not $featureFound) {
    $failures += 'this Windows image has neither the Client-ProjFS nor the Projected-FileSystem optional feature'
}

# 2. The user-mode library that the ufs host package loads is present.
$dll = Join-Path $env:SystemRoot 'System32\projectedfslib.dll'
if (Test-Path -LiteralPath $dll) {
    Write-Host "Found $dll"
} else {
    $failures += "$dll does not exist"
}

# 3. The PrjFlt filter driver is loaded. A feature that was enabled without a
# restart has the library but no loaded driver, and every mount then fails.
$filters = & fltmc.exe filters
if ($LASTEXITCODE -ne 0) {
    $failures += "fltmc filters exited with code $LASTEXITCODE (administrator privileges are required)"
} elseif ($filters | Select-String -SimpleMatch -Quiet 'PrjFlt') {
    Write-Host 'PrjFlt filter driver is loaded'
} else {
    $failures += 'the PrjFlt filter driver is not loaded; restart Windows after enabling the feature'
}

if ($failures.Count -gt 0) {
    foreach ($failure in $failures) {
        Write-Host "::error::ProjFS is not usable: $failure"
    }
    exit 1
}
Write-Host 'ProjFS is installed and usable'
