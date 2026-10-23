#!/usr/bin/env bash
# 回退到已安装版本。只切换程序，不执行数据库降级，不改写配置和数据。
set -Eeuo pipefail
umask 077

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

PREFIX=$DEFAULT_PREFIX
CONFIG_DIR=$DEFAULT_CONFIG_DIR
DATA_DIR=$DEFAULT_DATA_DIR
TIKU_CONFIG_DIR=$DEFAULT_TIKU_CONFIG_DIR
SERVICE_USER=$DEFAULT_SERVICE_USER
TIKU_USER=$DEFAULT_TIKU_USER
SKIP_TIKU=0
TARGET_VERSION=""
NO_SYSTEMD=0
NO_RESTART=0

usage() {
  cat <<'EOF'
用法：sudo ./deploy/rollback.sh --version VERSION [选项]

要求对应版本目录已存在于 PREFIX/releases 且 checksums.txt 校验通过。
本脚本不会执行任何数据库迁移或降级；旧程序能否读取当前 schema 需人工核对。
选项：
  --prefix DIR        程序版本目录（默认 /opt/byebyeocs）
  --version VERSION   目标已安装版本（必填）
  --no-systemd        不操作 systemd（隔离测试）
  --no-restart        不重启正在运行的服务
  -h, --help          显示帮助
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix) PREFIX=$(normalize_path "$2"); shift 2 ;;
    --version) TARGET_VERSION=$2; shift 2 ;;
    --no-systemd) NO_SYSTEMD=1; shift ;;
    --no-restart) NO_RESTART=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数：$1" ;;
  esac
done

[[ -n "$TARGET_VERSION" ]] || die "必须指定 --version；不会猜测目标版本"
safe_version "$TARGET_VERSION"
[[ "$PREFIX" != "/" ]] || die "拒绝使用根目录作为安装路径"
if [[ "$NO_SYSTEMD" != 1 && $EUID -ne 0 ]]; then
  die "回滚需要 root；隔离测试可使用 --no-systemd"
fi
for cmd in install cp mv ln readlink realpath sha256sum find sed mktemp chmod chown flock basename; do require_cmd "$cmd"; done

load_install_state "$PREFIX"
require_install_state "$PREFIX"
validate_user_name "$SERVICE_USER"
validate_user_name "$TIKU_USER"
current_version=$(basename "$(current_release_dir "$PREFIX")")
target="$PREFIX/releases/$TARGET_VERSION"

lock_prefix "$PREFIX"
cleanup() {
  restore_replaced_releases "$PREFIX"
  cleanup_temp_paths
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
cleanup_stale_prefix_artifacts "$PREFIX"
# 在锁内复核目标版本，避免与并发强制重装交错。
verify_installed_release "$target"
switch_current "$PREFIX" "$TARGET_VERSION"
install_units
restart_active_services

log "已回退 $current_version -> $TARGET_VERSION。"
log "未迁移或降级数据库；配置和数据保持原样。若旧版本与当前 schema 不兼容，请停止服务并按备份恢复预案处理。"
