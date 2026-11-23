#!/usr/bin/env pwsh
# Windows PowerShell 便捷入口；直接转发到跨平台脚本 scripts/dev.mjs。
$ErrorActionPreference = 'Stop'
if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
  [Console]::Error.WriteLine('错误：未找到 node 命令，请先安装 Node.js 22.12 或以上。')
  exit 1
}
$scriptPath = Join-Path $PSScriptRoot 'dev.mjs'
& node $scriptPath @args
exit $LASTEXITCODE
