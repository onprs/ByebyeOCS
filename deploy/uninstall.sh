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
reject_wide_path "程序路径" "$PREFIX"
if [[ "$PURGE" == 1 ]]; then
  reject_purge_path "配置路径" "$CONFIG_DIR"
  reject_purge_path "数据路径" "$DATA_DIR"
  reject_purge_path "题库配置路径" "$TIKU_CONFIG_DIR"
  for path in "$CONFIG_DIR" "$DATA_DIR" "$TIKU_CONFIG_DIR"; do
    [[ "$PREFIX" != "$path" ]] || die "程序路径不能与待删除目录相同：$path"
    [[ "$PREFIX" != "$path/"* ]] || die "程序路径位于待删除目录内，拒绝卸载：$path"
  done
  # 没有安装记录时只允许清理默认路径，避免攻略写错的自定义路径被递归删除。
  if [[ "${INSTALL_STATE_LOADED:-0}" != 1 ]]; then
    [[ "$CONFIG_DIR" == "$DEFAULT_CONFIG_DIR" ]] || die "--purge 自定义配置路径需要可读取的 install.env：$CONFIG_DIR"
    [[ "$DATA_DIR" == "$DEFAULT_DATA_DIR" ]] || die "--purge 自定义数据路径需要可读取的 install.env：$DATA_DIR"
    [[ "$TIKU_CONFIG_DIR" == "$DEFAULT_TIKU_CONFIG_DIR" ]] || die "--purge 自定义题库配置路径需要可读取的 install.env：$TIKU_CONFIG_DIR"
  fi
fi
if [[ "$NO_SYSTEMD" != 1 && $EUID -ne 0 ]]; then
  die "卸载需要 root；隔离测试可使用 --no-systemd"
fi
if [[ "$PURGE" == 1 && "$CONFIRM" != 1 ]]; then
  die "--purge 需要同时提供 --yes；默认保留数据"
fi
if [[ ! -e "$PREFIX/install.env" && ! -L "$PREFIX/current" && ! -d "$PREFIX/releases" ]]; then
  if [[ "$PURGE" == 1 ]]; then
    # 无安装记录时，上面的校验已将 --purge 限制为默认路径。
    log "未发现程序安装记录；仅按默认路径清理配置/数据目录"
  else
    log "未发现该前缀下的安装记录，无需卸载：$PREFIX"
    exit 0
  fi
fi

if [[ -d "$PREFIX" ]]; then
  lock_prefix "$PREFIX"
fi
cleanup_stale_prefix_artifacts "$PREFIX"
# 先校验 current 再停止服务，避免异常链接导致服务已停但程序未清理。
if [[ -L "$PREFIX/current" ]]; then
  resolved=$(readlink -f "$PREFIX/current" || true)
  case "$resolved" in
    "$PREFIX/releases/"*) rm -f -- "$PREFIX/current" ;;
    *) die "current 指向安装目录之外，拒绝处理" ;;
  esac
fi
remove_units
if [[ -d "$PREFIX/releases" ]]; then
  rm -rf -- "$PREFIX/releases"
fi
# 清理中断的强制替换备份，避免残留目录阻止前缀移除。
for path in "$PREFIX"/.replaced.*; do
  if [[ -e "$path" || -L "$path" ]]; then
    rm -rf -- "$path"
  fi
done
rm -f -- "$PREFIX/install.env"

if [[ "$PURGE" == 1 ]]; then
  rm -rf -- "$CONFIG_DIR" "$DATA_DIR"
  [[ "$TIKU_CONFIG_DIR" == "$CONFIG_DIR" ]] || rm -rf -- "$TIKU_CONFIG_DIR"
  log "已删除配置、密钥和数据目录；PostgreSQL 数据库未删除。"
else
  log "已移除程序和单元；配置、密钥、数据目录及数据库已保留。"
  log "保留：$CONFIG_DIR $DATA_DIR $TIKU_CONFIG_DIR"
fi
# 删除操作完成后再释放锁文件，缩小与并发安装交错的范围。
rm -f -- "$PREFIX/.deploy.lock"
rmdir -- "$PREFIX" 2>/dev/null || true
