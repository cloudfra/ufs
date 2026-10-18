# scripts/is-desktop-experience.ps1 - Report whether the host has the Windows
# Desktop Experience feature set.
#
# ProjFS (the Client-ProjFS optional feature, projectedfslib.dll and the
# PrjFlt filter) is not present in a Server Core image. Running the DISM and
# ServerManager probes in scripts/enable-projfs.ps1 there costs minutes and
# has been observed to take the self-hosted runner's connection down, so the
# steps that enable and verify ProjFS must not run on such a host.
#
# Detection is deliberately cheap: registry values only, with the
# ServerManager module as a fallback. It never enumerates features.
#
# Writes `desktopExperience=true|false` to $env:GITHUB_OUTPUT and always
# exits 0; the caller decides what to do with the answer.

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Continue'

function Get-DesktopExperience {
    $ntKey = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion'

    $installationType = $null
    $nt = Get-ItemProperty -LiteralPath $ntKey -ErrorAction SilentlyContinue
    if ($nt -and $nt.PSObject.Properties.Name -contains 'InstallationType') {
        $installationType = [string]$nt.InstallationType
    }
    Write-Host "InstallationType: $(if ($installationType) { $installationType } else { '<unknown>' })"

    # A client installation (Windows 10/11) always has the desktop feature
    # set, and there is no ServerLevels key to consult.
    if ($installationType -eq 'Client') {
        return $true
    }

    # On Windows Server this value is 1 when the Desktop Experience feature
    # set is installed and absent or 0 on Server Core.
    $levelsKey = Join-Path -Path $ntKey -ChildPath 'Server\ServerLevels'
    if (Test-Path -LiteralPath $levelsKey) {
        $levels = Get-ItemProperty -LiteralPath $levelsKey -ErrorAction SilentlyContinue
        if ($levels -and $levels.PSObject.Properties.Name -contains 'ServerDesktopExperience') {
            $level = [int]$levels.ServerDesktopExperience
            Write-Host "ServerDesktopExperience: $level"
            return $level -eq 1
        }
        Write-Host "ServerDesktopExperience is absent from $levelsKey"
    } else {
        Write-Host "$levelsKey is absent"
    }

    # Fallback for images without that key: the graphical shell features are
    # only installed together with Desktop Experience.
    if (Get-Command -Name Get-WindowsFeature -ErrorAction SilentlyContinue) {
        foreach ($name in 'Desktop-Experience', 'Server-Gui-Shell') {
            $feature = Get-WindowsFeature -Name $name -ErrorAction SilentlyContinue
            if ($feature) {
                Write-Host "Get-WindowsFeature ${name}: $($feature.InstallState)"
                if ([string]$feature.InstallState -eq 'Installed') {
                    return $true
                }
            }
        }
        return $false
    }

    Write-Host 'No ServerDesktopExperience value and no ServerManager module: reporting Desktop Experience as absent'
    return $false
}

$desktopExperience = Get-DesktopExperience
$value = if ($desktopExperience) { 'true' } else { 'false' }
Write-Host "desktopExperience=$value"

if ($env:GITHUB_OUTPUT) {
    Add-Content -LiteralPath $env:GITHUB_OUTPUT -Value "desktopExperience=$value" -Encoding ascii
} else {
    Write-Host 'GITHUB_OUTPUT is not set: the result is only printed'
}

exit 0
