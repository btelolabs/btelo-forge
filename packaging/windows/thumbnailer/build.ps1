<#
.SYNOPSIS
  Builds the thumbnail handler for both Windows architectures and puts it in internal\win, where the app embeds it.

.DESCRIPTION
  Run this after changing thumbnailer.c, and commit the DLLs, as with the Windows resources. It needs clang and
  lld-link; Rust's own lld-link will do. The DLLs use no C runtime: their only imports are named in the .def files
  here, so the same command makes both, with no Windows SDK for either.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File packaging\windows\thumbnailer\build.ps1
#>
param(
    [string]$Clang = 'clang',
    [string]$LldLink = 'lld-link'
)

$ErrorActionPreference = 'Stop'
$here = $PSScriptRoot
$out = Join-Path (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $here))) 'internal\win'
$temp = Join-Path ([IO.Path]::GetTempPath()) "btelo-thumbnailer-$PID"
New-Item -ItemType Directory -Force $temp | Out-Null

function Invoke-Tool([string]$tool, [string[]]$arguments) {
    & $tool @arguments
    if ($LASTEXITCODE -ne 0) { throw "$tool failed with exit code $LASTEXITCODE" }
}

try {
    foreach ($arch in @(@{ Go = 'amd64'; Clang = 'x86_64'; Machine = 'x64' }, @{ Go = 'arm64'; Clang = 'aarch64'; Machine = 'arm64' })) {
        $object = Join-Path $temp "thumbnailer-$($arch.Go).obj"
        Invoke-Tool $Clang @("--target=$($arch.Clang)-pc-windows-gnu", '-Os', '-std=c17', '-Wall', '-Werror', '-Wno-unused-function',
            '-ffreestanding', '-fno-builtin', '-fno-stack-protector', '-fno-asynchronous-unwind-tables', '-fno-ident',
            '-mguard=none', '-c', (Join-Path $here 'thumbnailer.c'), '-o', $object)
        $libraries = foreach ($name in 'kernel32', 'ole32', 'gdi32') {
            $lib = Join-Path $temp "$name-$($arch.Go).lib"
            Invoke-Tool $LldLink @('/lib', "/machine:$($arch.Machine)", "/def:$(Join-Path $here "$name.def")", "/out:$lib")
            $lib
        }
        Invoke-Tool $LldLink (@('/dll', '/noentry', '/nodefaultlib', '/release', '/brepro', '/opt:ref', "/machine:$($arch.Machine)",
                "/def:$(Join-Path $here 'exports.def')", "/out:$(Join-Path $out "thumbnailer_$($arch.Go).dll")", "/implib:$(Join-Path $temp 'unused.lib')", $object) + $libraries)
    }
}
finally {
    Remove-Item $temp -Recurse -Force -ErrorAction SilentlyContinue
}
