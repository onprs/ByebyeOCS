#!/usr/bin/env bash
# 回退到已安装版本。只切换程序，不执行数据库降级，不改写配置和数据。
set -Eeuo pipefail
umask 077

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

PREFIX=$DEFAULT_PREFIX
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
for cmd in install cp mv ln readlink realpath sha256sum find sed; do require_cmd "$cmd"; done

load_install_state "$PREFIX"
[[ -L "$PREFIX/current" ]] || die "未发现 current 符号链接"
target="$PREFIX/releases/$TARGET_VERSION"
verify_installed_release "$target"
current_version=$(basename "$(readlink -f "$PREFIX/current")")
switch_current "$PREFIX" "$TARGET_VERSION"
install_units
restart_active_services

log "已回退 $current_version -> $TARGET_VERSION。"
log "未迁移或降级数据库；配置和数据保持原样。若旧版本与当前 schema 不兼容，请停止服务并按备份恢复预案处理。"
