#!/usr/bin/env bash
# 从已构建发布目录安装。不会迁移数据库、创建默认账号、启动收费或覆盖现有配置/密钥。
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
NO_SYSTEMD=0
NO_SERVICE_USER=0
SKIP_TIKU=0
FORCE=0
USER_ORIGIN=""
ADMIN_ORIGIN=""
DATABASE_DSN_FILE=""

usage() {
  cat <<'EOF'
用法：sudo ./deploy/install.sh [选项]

从当前发布目录安装 ByebyeOCS。默认路径：
  /opt/byebyeocs、/etc/byebyeocs、/var/lib/byebyeocs、/etc/ocs-tiku

选项：
  --prefix DIR                 程序版本目录
  --config-dir DIR             主服务配置目录
  --data-dir DIR               主服务数据目录
  --tiku-config-dir DIR        题库配置目录
  --service-user NAME          主服务非 root 用户（默认 byebyeocs）
  --tiku-user NAME             题库非 root 用户（默认 ocs-tiku）
  --skip-tiku                  只安装主服务二进制和单元，不创建题库配置/单元
  --user-origin URL            首次安装时用于 config init 的用户端 HTTPS Origin
  --admin-origin URL           首次安装时用于 config init 的管理端 HTTPS Origin
  --database-dsn-file PATH     config init 引用的 PostgreSQL DSN 文件（不会创建或修改其内容）
  --force                      同版本已存在且校验不同时替换版本目录
  --no-systemd                 不安装 systemd 单元（仅用于打包/隔离测试）
  --no-service-user            不创建系统用户（仅用于非 root 临时目录测试）
  -h, --help                   显示本帮助

安全行为：
  - 安装前校验 checksums.txt、release.json 和 Linux 架构；许可证状态以 manifest 事实为准。
  - 只创建目录和示例文件；已有 config/keys 永不覆盖。
  - 不执行 migrate、admin create，不启用真实收费，不自动 enable/start 服务。
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix) PREFIX=$(normalize_path "$2"); shift 2 ;;
    --config-dir) CONFIG_DIR=$(normalize_path "$2"); shift 2 ;;
    --data-dir) DATA_DIR=$(normalize_path "$2"); shift 2 ;;
    --tiku-config-dir) TIKU_CONFIG_DIR=$(normalize_path "$2"); shift 2 ;;
    --service-user) SERVICE_USER=$2; shift 2 ;;
    --tiku-user) TIKU_USER=$2; shift 2 ;;
    --skip-tiku) SKIP_TIKU=1; shift ;;
    --user-origin) USER_ORIGIN=$2; shift 2 ;;
    --admin-origin) ADMIN_ORIGIN=$2; shift 2 ;;
    --database-dsn-file) DATABASE_DSN_FILE=$(normalize_path "$2"); shift 2 ;;
    --force) FORCE=1; shift ;;
    --no-systemd) NO_SYSTEMD=1; shift ;;
    --no-service-user) NO_SERVICE_USER=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数：$1（使用 --help 查看用法）" ;;
  esac
done

validate_user_name "$SERVICE_USER"
validate_user_name "$TIKU_USER"
[[ "$TIKU_USER" != "$SERVICE_USER" ]] || die "主服务和题库必须使用不同服务用户"
reject_wide_path "程序路径" "$PREFIX"
reject_wide_path "配置路径" "$CONFIG_DIR"
reject_wide_path "数据路径" "$DATA_DIR"
reject_wide_path "题库配置路径" "$TIKU_CONFIG_DIR"
[[ "$CONFIG_DIR" != "$DATA_DIR" ]] || die "配置目录和数据目录必须分开"
[[ "$CONFIG_DIR" != "$TIKU_CONFIG_DIR" ]] || die "主服务和题库配置目录必须分开"
[[ "$PREFIX" != "$CONFIG_DIR" && "$PREFIX" != "$DATA_DIR" ]] || die "程序前缀不能与配置/数据目录相同"

if [[ "$NO_SYSTEMD" == 1 && "$NO_SERVICE_USER" == 1 ]]; then
  : # 允许非 root 隔离测试/打包。
elif [[ $EUID -ne 0 ]]; then
  die "安装需要 root；仅隔离测试可使用 --no-systemd --no-service-user"
fi

for cmd in install cp mv ln readlink realpath sha256sum find sed uname mktemp chmod chown flock grep head; do
  require_cmd "$cmd"
done

VERSION=$(verify_release_root "$RELEASE_ROOT")
safe_version "$VERSION"
log "校验通过：ByebyeOCS $VERSION（$(uname -m)）"

# 同一 PREFIX 只允许一个安装/升级/回滚/卸载操作。
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

if [[ "$NO_SERVICE_USER" != 1 ]]; then
  ensure_user "$SERVICE_USER" "$DATA_DIR"
  [[ "$SKIP_TIKU" == 1 ]] || ensure_user "$TIKU_USER" "$TIKU_CONFIG_DIR"
fi

mapfile -t main_owner < <(owner_and_group "$SERVICE_USER")
OWNER=${main_owner[0]}
GROUP=${main_owner[1]}
TIKU_OWNER=""
TIKU_GROUP=""
if [[ "$SKIP_TIKU" != 1 ]]; then
  mapfile -t tiku_owner < <(owner_and_group "$TIKU_USER")
  TIKU_OWNER=${tiku_owner[0]}
  TIKU_GROUP=${tiku_owner[1]}
fi

install -d -m 0755 "$PREFIX" "$PREFIX/releases"
install_release_tree "$RELEASE_ROOT" "$PREFIX" "$VERSION" "$FORCE"
switch_current "$PREFIX" "$VERSION"

create_owned_dir "$CONFIG_DIR" "$OWNER" "$GROUP" 0700
create_owned_dir "$CONFIG_DIR/secrets" "$OWNER" "$GROUP" 0700
create_owned_dir "$DATA_DIR" "$OWNER" "$GROUP" 0700

# 示例文件只写 private 路径；已有文件绝不覆盖。
if [[ ! -e "$CONFIG_DIR/config.yaml.example" ]]; then
  install -m 0600 -o "$OWNER" -g "$GROUP" "$RELEASE_ROOT/configs/server.example.yaml" "$CONFIG_DIR/config.yaml.example"
fi
if [[ ! -e "$CONFIG_DIR/secrets/.gitkeep" ]]; then
  install -m 0600 -o "$OWNER" -g "$GROUP" /dev/null "$CONFIG_DIR/secrets/.gitkeep"
fi
if [[ ! -e "$CONFIG_DIR/backup.env.example" ]]; then
  install -m 0600 -o "$OWNER" -g "$GROUP" "$RELEASE_ROOT/deploy/backup.env.example" "$CONFIG_DIR/backup.env.example"
fi

if [[ "$SKIP_TIKU" != 1 ]]; then
  create_owned_dir "$TIKU_CONFIG_DIR" "$TIKU_OWNER" "$TIKU_GROUP" 0700
  create_owned_dir "$TIKU_CONFIG_DIR/secrets" "$TIKU_OWNER" "$TIKU_GROUP" 0700
  if [[ ! -e "$TIKU_CONFIG_DIR/ocs-tiku-fork.json.example" ]]; then
    install -m 0600 -o "$TIKU_OWNER" -g "$TIKU_GROUP" "$RELEASE_ROOT/configs/ocs-tiku.example.json" "$TIKU_CONFIG_DIR/ocs-tiku-fork.json.example"
  fi
fi

# 首次安装且提供了两个 Origin 时，可用正式 CLI 生成配置和独立密钥。
# config init 会拒绝覆盖；不会创建数据库、管理员或收费配置。
if [[ ! -e "$CONFIG_DIR/config.yaml" && -n "$USER_ORIGIN" && -n "$ADMIN_ORIGIN" ]]; then
  init_args=(config init --output "$CONFIG_DIR/config.yaml" --key-output "$CONFIG_DIR/secrets/application-v1.key"
    --user-origin "$USER_ORIGIN" --admin-origin "$ADMIN_ORIGIN"
    --data-dir "$DATA_DIR" --admin-assets-dir "$PREFIX/current/apps/admin"
    --client-assets-dir "$PREFIX/current/apps/client-web")
  if [[ -n "$DATABASE_DSN_FILE" ]]; then
    init_args+=(--database-dsn-file "$DATABASE_DSN_FILE")
  fi
  "$PREFIX/current/bin/byebyeocs" "${init_args[@]}"
  # 公开运行参数统一为 95 秒，覆盖题库 provider 的内部 90 秒超时边界。
  sed -i 's/^    timeout: .*$/    timeout: 1m35s/' "$CONFIG_DIR/config.yaml"
  if [[ "$NO_SERVICE_USER" != 1 ]]; then
    chown -R "$OWNER:$GROUP" "$CONFIG_DIR/config.yaml" "$CONFIG_DIR/secrets"
  fi
  log "已生成 $CONFIG_DIR/config.yaml；请创建数据库 DSN 后再显式执行 migrate up 和 admin create。"
else
  log "未生成活动配置：请在 $CONFIG_DIR 下按示例创建 config.yaml 和 secrets（已有文件不会被覆盖）。"
fi

install_units

# 记录本次安装路径，供升级/回滚/卸载使用；不包含密钥或数据库连接。
STATE_FILE="$PREFIX/install.env"
STATE_TMP=$(mktemp "$PREFIX/.install.env.XXXXXXXX")
register_temp_path "$STATE_TMP"
printf 'PREFIX=%q\nCONFIG_DIR=%q\nDATA_DIR=%q\nTIKU_CONFIG_DIR=%q\nSERVICE_USER=%q\nTIKU_USER=%q\nSKIP_TIKU=%q\n' \
  "$PREFIX" "$CONFIG_DIR" "$DATA_DIR" "$TIKU_CONFIG_DIR" "$SERVICE_USER" "$TIKU_USER" "$SKIP_TIKU" >"$STATE_TMP"
chmod 0600 "$STATE_TMP"
mv -Tf -- "$STATE_TMP" "$STATE_FILE"
forget_temp_path "$STATE_TMP"

log ""
log "安装完成，但服务未启动，数据库未迁移，管理员未创建，真实收费未启用。"
log "下一步："
log "  1. 按 $CONFIG_DIR/config.yaml.example 配置数据库和两个 HTTPS Origin。"
log "  2. sudo -u $SERVICE_USER $PREFIX/current/bin/byebyeocs migrate up --config $CONFIG_DIR/config.yaml"
log "  3. sudo -u $SERVICE_USER $PREFIX/current/bin/byebyeocs admin create --config $CONFIG_DIR/config.yaml --username ... --password-file ... --totp-output-file $CONFIG_DIR/secrets/admin-totp-setup.json"
log "  4. 配置反向代理（参考发布包 configs/nginx-*.example.conf）。"
log "  5. systemctl enable --now $SERVICE_NAME"
[[ "$SKIP_TIKU" == 1 ]] || log "题库：配置 $TIKU_CONFIG_DIR/ocs-tiku-fork.json 和独立数据库后再启用 $TIKU_SERVICE_NAME。"
