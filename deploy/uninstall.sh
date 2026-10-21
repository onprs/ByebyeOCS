#!/usr/bin/env bash
# 卸载程序和 systemd 单元，默认保留配置、密钥和数据目录，永不删除 PostgreSQL 数据库。
set -Eeuo pipefail
umask 077

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

PREFIX=$DEFAULT_PREFIX
CONFIG_DIR=$DEFAULT_CONFIG_DIR
DATA_DIR=$DEFAULT_DATA_DIR
TIKU_CONFIG_DIR=$DEFAULT_TIKU_CONFIG_DIR
NO_SYSTEMD=0
PURGE=0
CONFIRM=0

usage() {
  cat <<'EOF'
用法：sudo ./deploy/uninstall.sh [选项]

默认只移除程序版本、current 和 systemd 单元，保留全部配置、密钥、数据目录和数据库。
选项：
  --prefix DIR         程序版本目录
  --config-dir DIR     主服务配置目录
  --data-dir DIR       主服务数据目录
  --tiku-config-dir DIR 题库配置目录
  --purge              同时删除配置/密钥/数据目录（仍不会删除数据库）
  --yes                与 --purge 一起使用以确认删除
  --no-systemd         不操作 systemd（隔离测试）
  -h, --help           显示帮助
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix) PREFIX=$(normalize_path "$2"); shift 2 ;;
    --config-dir) CONFIG_DIR=$(normalize_path "$2"); shift 2 ;;
    --data-dir) DATA_DIR=$(normalize_path "$2"); shift 2 ;;
    --tiku-config-dir) TIKU_CONFIG_DIR=$(normalize_path "$2"); shift 2 ;;
    --purge) PURGE=1; shift ;;
    --yes) CONFIRM=1; shift ;;
    --no-systemd) NO_SYSTEMD=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数：$1" ;;
  esac
done

load_install_state "$PREFIX"
[[ "$PREFIX" != "/" && "$PREFIX" != "/opt" && "$PREFIX" != "/usr" && "$PREFIX" != "/var" ]] || die "拒绝从过宽的路径卸载程序"
[[ "$CONFIG_DIR" != "/" && "$DATA_DIR" != "/" && "$TIKU_CONFIG_DIR" != "/" ]] || die "拒绝删除根目录"
if [[ "$NO_SYSTEMD" != 1 && $EUID -ne 0 ]]; then
  die "卸载需要 root；隔离测试可使用 --no-systemd"
fi
if [[ "$PURGE" == 1 && "$CONFIRM" != 1 ]]; then
  die "--purge 需要同时提供 --yes；默认保留数据"
fi

remove_units
if [[ -L "$PREFIX/current" ]]; then
  resolved=$(readlink -f "$PREFIX/current" || true)
  case "$resolved" in
    "$PREFIX/releases/"*) rm -f -- "$PREFIX/current" ;;
    *) die "current 指向安装目录之外，拒绝处理" ;;
  esac
fi
if [[ -d "$PREFIX/releases" ]]; then
  rm -rf -- "$PREFIX/releases"
fi
rm -f -- "$PREFIX/install.env"
rmdir -- "$PREFIX" 2>/dev/null || true

if [[ "$PURGE" == 1 ]]; then
  rm -rf -- "$CONFIG_DIR" "$DATA_DIR"
  [[ "$TIKU_CONFIG_DIR" == "$CONFIG_DIR" ]] || rm -rf -- "$TIKU_CONFIG_DIR"
  log "已删除配置、密钥和数据目录；PostgreSQL 数据库未删除。"
else
  log "已移除程序和单元；配置、密钥、数据目录及数据库已保留。"
  log "保留：$CONFIG_DIR $DATA_DIR $TIKU_CONFIG_DIR"
fi
