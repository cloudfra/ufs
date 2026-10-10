# scripts/enable-projfs.ps1 - Enable Windows Projected File System feature.
#
# Microsoft documents one way: the optional feature Client-ProjFS
# (https://learn.microsoft.com/windows/win32/projfs/enabling-windows-projected-file-system).
# Other sources name the feature Projected-FileSystem on Windows Server, and
# older notes use FS-Projectedfs with Install-WindowsFeature, so those names
# are tried too.
#
# The script tries, in order, and stops at the first that works:
#   1. Enable-WindowsOptionalFeature (DISM PowerShell module), each name
#   2. dism.exe /Online /Enable-Feature, each name
#   3. Install-WindowsFeature (Server Manager module), each name
#   4. dism.exe again, from a scheduled task that runs as SYSTEM outside of
#      the process tree of the caller, each name
#
# It prints a lot of diagnostics on purpose: it runs unattended on CI runners,
# where its output is the only way to find out why a step had no effect.
# It never fails the build; scripts/verify-projfs.ps1 decides if ProjFS is usable.

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Continue'

$optionalFeatureNames = @('Client-ProjFS', 'Projected-FileSystem')
$serverFeatureNames = @('FS-Projectedfs', 'Projected-FileSystem', 'Client-ProjFS')

function Write-Section([string]$title) {
    Write-Host ''
    Write-Host "::group::$title"
}

function Close-Section {
    Write-Host "::endgroup::"
}

# Invoke-Logged runs a script block, prints everything it writes and reports an
# error instead of throwing, so one failing probe does not hide the next.
function Invoke-Logged([string]$what, [scriptblock]$block) {
    Write-Host ">>> $what"
    try {
        & $block 2>&1 | Out-String -Width 250 | ForEach-Object { Write-Host $_.TrimEnd() }
    } catch {
        Write-Host "!!! $what failed: $($_.Exception.GetType().FullName): $($_.Exception.Message)"
    }
}

function Test-ProjFSLibrary {
    return (Test-Path -LiteralPath (Join-Path $env:SystemRoot 'System32\projectedfslib.dll'))
}

# Get-OptionalFeatureState returns the state ('Enabled', 'Disabled', ...) of
# the first ProjFS optional feature the image knows, or $null if it knows none.
function Get-OptionalFeatureState {
    foreach ($name in $optionalFeatureNames) {
        try {
            $feature = Get-WindowsOptionalFeature -Online -FeatureName $name -ErrorAction Stop
            if ($feature) {
                return [string]$feature.State
            }
        } catch {
            Write-Host "Get-WindowsOptionalFeature $name failed: $($_.Exception.Message)"
        }
    }
    return $null
}

function Write-Diagnostics([string]$title) {
    Write-Section $title

    Invoke-Logged 'operating system' {
        Get-CimInstance -ClassName Win32_OperatingSystem |
            Format-List Caption, Version, BuildNumber, OSArchitecture, OperatingSystemSKU, ProductType, LastBootUpTime
    }
    Invoke-Logged 'installation type (Server, Server Core, Client)' {
        Get-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion' |
            Format-List ProductName, EditionID, InstallationType, DisplayVersion, CurrentBuild, UBR
    }
    Invoke-Logged 'process' {
        $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
        $principal = [Security.Principal.WindowsPrincipal]::new($identity)
        "user:            $($identity.Name)"
        "administrator:   $($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator))"
        "64-bit process:  $([Environment]::Is64BitProcess)"
        "64-bit OS:       $([Environment]::Is64BitOperatingSystem)"
        "session id:      $([Diagnostics.Process]::GetCurrentProcess().SessionId)"
        "PowerShell:      $($PSVersionTable.PSVersion) ($($PSVersionTable.PSEdition))"
        "executable:      $([Diagnostics.Process]::GetCurrentProcess().MainModule.FileName)"
        "SystemRoot:      $env:SystemRoot"
        "PSModulePath:    $env:PSModulePath"
    }
    Invoke-Logged 'modules that manage features' {
        Get-Module -ListAvailable -Name Dism, ServerManager | Format-Table Name, Version, ModuleBase -AutoSize
    }
    Invoke-Logged 'commands that manage features' {
        Get-Command -Name Get-WindowsOptionalFeature, Enable-WindowsOptionalFeature, Get-WindowsFeature, Install-WindowsFeature, dism.exe, fltmc.exe, schtasks.exe -ErrorAction SilentlyContinue |
            Format-Table Name, CommandType, Source -AutoSize
    }
    foreach ($name in $optionalFeatureNames) {
        Invoke-Logged "Get-WindowsOptionalFeature -Online -FeatureName $name" {
            Get-WindowsOptionalFeature -Online -FeatureName $name -ErrorAction Stop | Format-List *
        }
        Invoke-Logged "dism.exe /Online /Get-FeatureInfo /FeatureName:$name" {
            & dism.exe /Online /English /Get-FeatureInfo "/FeatureName:$name"
            "dism.exe exit code: $LASTEXITCODE"
        }
    }
    Invoke-Logged 'Get-WindowsOptionalFeature -Online: every feature' {
        $all = @(Get-WindowsOptionalFeature -Online -ErrorAction Stop)
        "total optional features: $($all.Count)"
        $all | Sort-Object FeatureName | Format-Table FeatureName, State -AutoSize
    }
    Invoke-Logged 'dism.exe /Online /Get-Features: lines like Proj' {
        $lines = @(& dism.exe /Online /English /Get-Features /Format:Table)
        "dism.exe exit code: $LASTEXITCODE, output lines: $($lines.Count)"
        $lines | Select-String -Pattern 'Proj'
    }
    Invoke-Logged 'Get-WindowsFeature: every feature' {
        if (Get-Command Get-WindowsFeature -ErrorAction SilentlyContinue) {
            $all = @(Get-WindowsFeature)
            "total server features: $($all.Count)"
            $all | Sort-Object Name | Format-Table Name, InstallState, DisplayName -AutoSize
        } else {
            'Get-WindowsFeature is not available'
        }
    }
    Invoke-Logged 'Get-WindowsCapability -Online: number of capabilities, and the ones like *Proj* or *AppCompat*' {
        $all = @(Get-WindowsCapability -Online -ErrorAction Stop)
        "total capabilities: $($all.Count)"
        $all | Where-Object { $_.Name -like '*Proj*' -or $_.Name -like '*AppCompat*' } |
            Format-Table Name, State -AutoSize
    }
    Invoke-Logged 'ProjFS files' {
        foreach ($file in 'System32\projectedfslib.dll', 'SysWOW64\projectedfslib.dll', 'System32\drivers\prjflt.sys') {
            $path = Join-Path $env:SystemRoot $file
            if (Test-Path -LiteralPath $path) {
                $item = Get-Item -LiteralPath $path
                "present  $path ($($item.Length) bytes, $($item.VersionInfo.FileVersion))"
            } else {
                "missing  $path"
            }
        }
        'component store (WinSxS) directories like *projfs* or *prjflt*:'
        Get-ChildItem -LiteralPath (Join-Path $env:SystemRoot 'WinSxS') -Directory -ErrorAction SilentlyContinue |
            Where-Object { $_.Name -like '*projfs*' -or $_.Name -like '*prjflt*' -or $_.Name -like '*projected*' } |
            ForEach-Object { "  $($_.Name)" }
    }
    Invoke-Logged 'PrjFlt driver service' {
        & sc.exe query prjflt
        "sc.exe exit code: $LASTEXITCODE"
        & sc.exe qc prjflt
    }
    Invoke-Logged 'fltmc filters' {
        & fltmc.exe filters
        "fltmc.exe exit code: $LASTEXITCODE"
    }
    Invoke-Logged 'pending restart markers' {
        foreach ($key in 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending',
            'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired') {
            "$(if (Test-Path -LiteralPath $key) { 'present' } else { 'absent ' })  $key"
        }
    }

    Close-Section
}

function Write-DismLogTail {
    Write-Section 'Tail of the DISM log'
    Invoke-Logged 'last 60 lines of dism.log' {
        $log = Join-Path $env:SystemRoot 'Logs\DISM\dism.log'
        if (Test-Path -LiteralPath $log) {
            Get-Content -LiteralPath $log -Tail 60
        } else {
            "$log does not exist"
        }
    }
    Close-Section
}

# Test-Enabled reports whether ProjFS looks enabled, and says why.
function Test-Enabled([string]$after) {
    $state = Get-OptionalFeatureState
    $library = Test-ProjFSLibrary
    Write-Host "After ${after}: optional feature state = $(if ($state) { $state } else { '<unknown>' }), projectedfslib.dll present = $library"
    return ($state -eq 'Enabled' -or $library)
}

function Enable-WithCmdlet {
    Write-Section 'Attempt 1: Enable-WindowsOptionalFeature'
    foreach ($name in $optionalFeatureNames) {
        Invoke-Logged "Enable-WindowsOptionalFeature -FeatureName $name" {
            Enable-WindowsOptionalFeature -Online -FeatureName $name -All -NoRestart -ErrorAction Stop | Format-List *
        }
    }
    Close-Section
}

function Enable-WithDism {
    Write-Section 'Attempt 2: dism.exe /Enable-Feature'
    foreach ($name in $optionalFeatureNames) {
        Invoke-Logged "dism.exe /Enable-Feature /FeatureName:$name" {
            & dism.exe /Online /English /Enable-Feature "/FeatureName:$name" /All /NoRestart
            "dism.exe exit code: $LASTEXITCODE (0 = done, 3010 = done, restart required)"
        }
    }
    Close-Section
}

function Enable-WithServerManager {
    Write-Section 'Attempt 3: Install-WindowsFeature'
    if (Get-Command Install-WindowsFeature -ErrorAction SilentlyContinue) {
        foreach ($name in $serverFeatureNames) {
            Invoke-Logged "Install-WindowsFeature -Name $name" {
                Install-WindowsFeature -Name $name -ErrorAction Stop | Format-List *
            }
        }
    } else {
        Write-Host 'Install-WindowsFeature is not available'
    }
    Close-Section
}

# Enable-WithScheduledTask runs dism.exe from a scheduled task as SYSTEM. The
# task is started by the Task Scheduler service, so it does not inherit the
# token, the job object or the environment of the CI runner.
function Enable-WithScheduledTask {
    Write-Section 'Attempt 4: dism.exe from a scheduled task running as SYSTEM'

    $taskName = "ufs-enable-projfs-$PID"
    $workDir = Join-Path $env:SystemRoot 'Temp'
    $logFile = Join-Path $workDir "$taskName.log"
    $doneFile = Join-Path $workDir "$taskName.done"
    $cmdFile = Join-Path $workDir "$taskName.cmd"

    # A .cmd file keeps the task's command line free of quoting problems.
    $commands = @(
        '@echo off',
        "echo task started as %USERNAME% > `"$logFile`"",
        "whoami /user /priv >> `"$logFile`" 2>&1"
    )
    foreach ($name in $optionalFeatureNames) {
        $commands += "echo dism.exe /Enable-Feature /FeatureName:$name >> `"$logFile`""
        $commands += "`"%SystemRoot%\System32\dism.exe`" /Online /English /Enable-Feature /FeatureName:$name /All /NoRestart >> `"$logFile`" 2>&1"
        $commands += "echo dism.exe exit code: %ERRORLEVEL% >> `"$logFile`""
    }
    $commands += "echo done > `"$doneFile`""

    try {
        Set-Content -LiteralPath $cmdFile -Value $commands -Encoding Ascii
        Remove-Item -LiteralPath $logFile, $doneFile -ErrorAction SilentlyContinue

        Invoke-Logged 'schtasks /Create' {
            & schtasks.exe /Create /TN $taskName /TR "`"$cmdFile`"" /SC ONCE /ST 00:00 /RU SYSTEM /RL HIGHEST /F
            "schtasks.exe exit code: $LASTEXITCODE"
        }
        Invoke-Logged 'schtasks /Run' {
            & schtasks.exe /Run /TN $taskName
            "schtasks.exe exit code: $LASTEXITCODE"
        }

        # Enabling a feature takes from a few seconds to a few minutes.
        $deadline = (Get-Date).AddMinutes(8)
        while (-not (Test-Path -LiteralPath $doneFile) -and (Get-Date) -lt $deadline) {
            Start-Sleep -Seconds 5
        }
        if (Test-Path -LiteralPath $doneFile) {
            Write-Host 'The scheduled task finished.'
        } else {
            Write-Host '!!! The scheduled task did not finish within 8 minutes.'
        }

        Invoke-Logged 'schtasks /Query' {
            & schtasks.exe /Query /TN $taskName /V /FO LIST
        }
        Invoke-Logged 'output of the scheduled task' {
            if (Test-Path -LiteralPath $logFile) {
                Get-Content -LiteralPath $logFile
            } else {
                "$logFile does not exist: the task did not start"
            }
        }
    } catch {
        Write-Host "!!! scheduled task attempt failed: $($_.Exception.Message)"
    } finally {
        & schtasks.exe /Delete /TN $taskName /F 2>&1 | Out-Null
        Remove-Item -LiteralPath $cmdFile, $logFile, $doneFile -ErrorAction SilentlyContinue
    }

    Close-Section
}

# Start-ProjFSDriver loads the PrjFlt filter driver if the feature was enabled
# without a restart. It is harmless if the driver is already loaded.
function Start-ProjFSDriver {
    Write-Section 'Load the PrjFlt filter driver'
    Invoke-Logged 'fltmc load prjflt' {
        if (& fltmc.exe filters | Select-String -SimpleMatch -Quiet 'PrjFlt') {
            'PrjFlt is already loaded'
        } else {
            & fltmc.exe load prjflt
            "fltmc.exe exit code: $LASTEXITCODE"
        }
    }
    Close-Section
}

Write-Diagnostics 'Diagnostics before enabling ProjFS'

$enabled = Test-Enabled 'the initial check'
if ($enabled) {
    Write-Host 'ProjFS is already enabled.'
} else {
    $attempts = @(
        @{ Name = 'Enable-WindowsOptionalFeature'; Run = { Enable-WithCmdlet } },
        @{ Name = 'dism.exe'; Run = { Enable-WithDism } },
        @{ Name = 'Install-WindowsFeature'; Run = { Enable-WithServerManager } },
        @{ Name = 'the scheduled task'; Run = { Enable-WithScheduledTask } }
    )
    foreach ($attempt in $attempts) {
        & $attempt.Run
        $enabled = Test-Enabled $attempt.Name
        if ($enabled) {
            Write-Host "ProjFS was enabled by $($attempt.Name)."
            break
        }
    }
}

if ($enabled) {
    Start-ProjFSDriver
} else {
    Write-DismLogTail
    $installationType = (Get-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion').InstallationType
    if ($null -eq (Get-OptionalFeatureState)) {
        # No tool knows the feature, so the image does not contain it and
        # nothing can enable it. That is the case on a Server Core installation.
        Write-Host "::warning::ProjFS cannot be enabled: this Windows image (installation type '$installationType') contains none of the features $($optionalFeatureNames -join ', '). Use an installation that has one, such as Windows Server with Desktop Experience or Windows 10/11."
    } else {
        Write-Host '::warning::ProjFS could not be enabled - see the diagnostics in the log of this step'
    }
}

Write-Diagnostics 'Diagnostics after enabling ProjFS'

# Diagnostics and attempts leave non-zero exit codes behind; this script reports, it does not fail.
exit 0
