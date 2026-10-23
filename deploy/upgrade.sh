#!/usr/bin/env bash
# 原子切换新版本。不会执行数据库迁移、不会生成账号、不会改写配置和数据。
set -Eeuo pipefail
umask 077

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

RELEASE_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)
PREFIX=$DEFAULT_PREFIX
CONFIG_DIR=$DEFAULT_CONFIG_DIR
DATA_DIR=$DEFAULT_DATA_DIR
TIKU_CONFIG_DIR=$DEFAULT_TIKU_CONFIG_DIR
SERVICE_USER=$DEFAULT_SERVICE_USER
TIKU_USER=$DEFAULT_TIKU_USER
SKIP_TIKU=0
NO_SYSTEMD=0
NO_RESTART=0
FORCE=0

usage() {
  cat <<'EOF'
用法：sudo ./deploy/upgrade.sh [选项]

从当前发布目录切换已安装版本。不会迁移数据库、不会降级数据库 schema。
选项：
  --prefix DIR        程序版本目录（默认 /opt/byebyeocs）
  --service-user NAME 主服务用户（默认 byebyeocs）
  --force             同版本已存在且校验不同时替换版本目录
  --no-systemd        不操作 systemd（隔离测试）
  --no-restart        不重启正在运行的服务
  -h, --help          显示帮助
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix) PREFIX=$(normalize_path "$2"); shift 2 ;;
    --service-user) SERVICE_USER=$2; shift 2 ;;
    --force) FORCE=1; shift ;;
    --no-systemd) NO_SYSTEMD=1; shift ;;
    --no-restart) NO_RESTART=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数：$1" ;;
  esac
done

[[ "$PREFIX" != "/" ]] || die "拒绝使用根目录作为安装路径"
if [[ "$NO_SYSTEMD" != 1 && $EUID -ne 0 ]]; then
  die "升级需要 root；隔离测试可使用 --no-systemd"
fi
for cmd in install cp mv ln readlink realpath sha256sum find sed uname mktemp chmod chown flock basename; do require_cmd "$cmd"; done

load_install_state "$PREFIX"
require_install_state "$PREFIX"
validate_user_name "$SERVICE_USER"
validate_user_name "$TIKU_USER"
current_dir=$(current_release_dir "$PREFIX")
VERSION=$(verify_release_root "$RELEASE_ROOT")
safe_version "$VERSION"
old_version=$(basename "$current_dir")

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

install_release_tree "$RELEASE_ROOT" "$PREFIX" "$VERSION" "$FORCE"
install_units
switch_current "$PREFIX" "$VERSION"
restart_active_services

log "已切换 $old_version -> $VERSION。"
log "配置和数据未改写；未执行数据库迁移。数据库 schema 不会回退。"
log "如新版本需要迁移，请先备份并显式执行 migrate status/up；失败时使用 rollback.sh 回退程序。"
