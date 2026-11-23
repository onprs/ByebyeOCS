#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd "$(dirname "$0")" && pwd)"
if ! command -v node >/dev/null 2>&1; then
  printf '错误：未找到 node 命令，请先安装 Node.js 22.12 或以上。\n' >&2
  exit 1
fi
exec node "$script_dir/dev.mjs" "$@"
