<#
.SYNOPSIS
  Builds Btelo Forge from the working tree and opens it: a development run without a release.

.DESCRIPTION
  Compiles ./cmd/btelo-forge the way the release does on Windows (a GUI program, no console), stamped
  dev-<commit>[-dirty], into build\dev, then starts it. Go's build cache keeps a rebuild to a few seconds. A build
  error is shown in a message box, since the script normally runs with no window.

  The running copy is renamed aside before the new one is moved in, so a build works while an older one is open.

  -InstallShortcut puts a shortcut on the desktop that runs this script.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev.ps1 -InstallShortcut
#>
param(
    [switch]$InstallShortcut,
    [Parameter(ValueFromRemainingArguments)][string[]]$AppArgs
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$out = Join-Path $repo 'build\dev'
$exe = Join-Path $out 'Btelo Forge.exe'

function Show-Error([string]$text) {
    Add-Type -AssemblyName System.Windows.Forms
    [void][System.Windows.Forms.MessageBox]::Show($text, 'Btelo Forge（开发版）',
        [System.Windows.Forms.MessageBoxButtons]::OK, [System.Windows.Forms.MessageBoxIcon]::Error)
}

function Get-Version {
    $commit = (& git -C $repo rev-parse --short HEAD 2>$null)
    if ($LASTEXITCODE -ne 0 -or -not $commit) { return 'dev' }
    $dirty = (& git -C $repo status --porcelain 2>$null)
    if ($dirty) { return "dev-$commit-dirty" }
    return "dev-$commit"
}

function Build {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw '找不到 go 命令：请安装 Go 1.27，并确认它在 PATH 里。'
    }
    New-Item -ItemType Directory -Force $out | Out-Null
    $next = Join-Path $out 'next.exe'
    $log = Join-Path $out 'build.log'
    $ldflags = "-H windowsgui -X main.version=$(Get-Version)"
    $go = Start-Process go -ArgumentList 'build', '-ldflags', "`"$ldflags`"", '-o', "`"$next`"", './cmd/btelo-forge' `
        -WorkingDirectory $repo -NoNewWindow -Wait -PassThru -RedirectStandardError $log
    if ($go.ExitCode -ne 0) {
        $errors = (Get-Content $log -Encoding UTF8 -TotalCount 40) -join "`n"
        throw "编译失败（go build 退出码 $($go.ExitCode)）：`n`n$errors"
    }
    # A running exe cannot be overwritten but can be renamed; the copy set aside last time is removed once it
    # has closed.
    $old = Join-Path $out 'old.exe'
    Remove-Item $old -Force -ErrorAction SilentlyContinue
    if (Test-Path $exe) {
        if (Test-Path $old) { $old = Join-Path $out "old-$([guid]::NewGuid().ToString('N')).exe" }
        Move-Item $exe $old
    }
    Move-Item $next $exe
    Get-ChildItem $out -Filter 'old-*.exe' | Remove-Item -Force -ErrorAction SilentlyContinue
    # The build from before the app was renamed, once it is no longer running.
    Remove-Item (Join-Path $out 'Btelo Video Cut.exe') -Force -ErrorAction SilentlyContinue
}

try {
    Build
    if ($InstallShortcut) {
        $link = Join-Path ([Environment]::GetFolderPath('Desktop')) 'Btelo Forge（开发版）.lnk'
        $shell = New-Object -ComObject WScript.Shell
        $shortcut = $shell.CreateShortcut($link)
        $shortcut.TargetPath = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
        $shortcut.Arguments = "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$PSCommandPath`""
        $shortcut.WorkingDirectory = $repo
        $shortcut.IconLocation = "$exe,0"
        $shortcut.Description = '从源码编译并打开 Btelo Forge'
        $shortcut.WindowStyle = 7
        $shortcut.Save()
        # The shortcut from before the app was renamed, which this one replaces.
        Remove-Item (Join-Path ([Environment]::GetFolderPath('Desktop')) 'Btelo Video Cut（开发版）.lnk') -Force -ErrorAction SilentlyContinue
        Write-Host "桌面快捷方式已创建：$link"
        return
    }
    if ($AppArgs) {
        Start-Process $exe -ArgumentList $AppArgs -WorkingDirectory $repo
    } else {
        Start-Process $exe -WorkingDirectory $repo
    }
} catch {
    if ($InstallShortcut) { throw }
    Show-Error $_.Exception.Message
    exit 1
}
