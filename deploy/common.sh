#!/usr/bin/env bash
# 公共安装/升级函数。脚本只面向 Linux + systemd；测试可用 --no-systemd --no-service-user。
set -Eeuo pipefail
umask 077

DEFAULT_PREFIX=/opt/byebyeocs
DEFAULT_CONFIG_DIR=/etc/byebyeocs
DEFAULT_DATA_DIR=/var/lib/byebyeocs
DEFAULT_TIKU_CONFIG_DIR=/etc/ocs-tiku
DEFAULT_SERVICE_USER=byebyeocs
DEFAULT_TIKU_USER=ocs-tiku
SERVICE_NAME=byebyeocs.service
TIKU_SERVICE_NAME=ocs-tiku.service

die() { printf '错误：%s\n' "$*" >&2; exit 1; }
log() { printf '%s\n' "$*"; }
require_cmd() { command -v "$1" >/dev/null 2>&1 || die "缺少命令：$1"; }

script_dir() {
  local source=${BASH_SOURCE[1]:-${BASH_SOURCE[0]}}
  (cd "$(dirname "$source")" && pwd)
}

normalize_path() {
  local value=$1
  [[ -n "$value" ]] || die "路径不能为空"
  [[ "$value" == /* ]] || die "路径必须为绝对路径：$value"
  [[ "$value" != *$'\n'* && "$value" != *$'\r'* && "$value" != *$'\t'* ]] || die "路径包含控制字符"
  [[ "$value" =~ ^[A-Za-z0-9._/+-]+$ ]] || die "路径只允许字母、数字、点、下划线、斜杠、加号和连字符：$value"
  realpath -m -- "$value"
}

validate_user_name() {
  [[ "$1" =~ ^[a-z_][a-z0-9_-]{0,31}$ ]] || die "服务用户名称无效：$1"
}

safe_version() {
  [[ "$1" =~ ^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$ ]] || die "版本号无效：$1"
}

read_manifest_field() {
  local root=$1 key=$2
  sed -n "s/^.*\"${key}\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*$/\\1/p" "$root/release.json" | head -n 1
}

verify_release_root() {
  local root=$1 expected_version=${2:-}
  [[ -d "$root" ]] || die "发布目录不存在：$root"
  [[ -f "$root/release.json" ]] || die "发布目录缺少 release.json"
  [[ -f "$root/checksums.txt" ]] || die "发布目录缺少 checksums.txt"
  [[ -x "$root/bin/byebyeocs" ]] || die "发布目录缺少可执行 bin/byebyeocs"
  [[ -x "$root/bin/ocs-tiku-fork" ]] || die "发布目录缺少可执行 bin/ocs-tiku-fork"
  [[ -f "$root/apps/admin/index.html" ]] || die "发布目录缺少管理员静态应用"
  [[ -f "$root/apps/client-web/index.html" ]] || die "发布目录缺少 Flutter Web 静态应用"
  local version goos goarch
  version=$(read_manifest_field "$root" version)
  goos=$(read_manifest_field "$root" goos)
  goarch=$(read_manifest_field "$root" goarch)
  safe_version "$version"
  [[ -z "$expected_version" || "$version" == "$expected_version" ]] || die "发布版本与期望不一致：$version"
  [[ "$goos" == linux ]] || die "发布包不是 Linux 包：$goos"
  local machine
  machine=$(uname -m)
  case "$machine" in
    x86_64|amd64) machine=amd64 ;;
    aarch64|arm64) machine=arm64 ;;
    *) die "不支持的机器架构：$machine" ;;
  esac
  [[ "$goarch" == "$machine" ]] || die "发布包架构 $goarch 与当前机器 $machine 不一致"
  if find "$root" -type l -print -quit | grep -q .; then
    die "发布目录包含符号链接，拒绝安装"
  fi
  (cd "$root" && sha256sum -c --quiet checksums.txt) || die "发布包校验失败"
  printf '%s\n' "$version"
}

verify_installed_release() {
  local target=$1
  [[ -d "$target" ]] || die "版本目录不存在：$target"
  (cd "$target" && sha256sum -c --quiet checksums.txt) || die "已安装版本校验失败：$target"
}

ensure_group() {
  local group=$1
  if ! getent group "$group" >/dev/null 2>&1; then
    groupadd --system "$group"
  fi
}

ensure_user() {
  local user=$1 home=$2
  validate_user_name "$user"
  if id -u "$user" >/dev/null 2>&1; then
    return 0
  fi
  ensure_group "$user"
  local shell_path
  shell_path=$(command -v nologin 2>/dev/null || printf '/usr/sbin/nologin')
  useradd --system --gid "$user" --home-dir "$home" --no-create-home --shell "$shell_path" "$user"
}

owner_and_group() {
  if [[ "${NO_SERVICE_USER:-0}" == 1 ]]; then
    id -un
    id -gn
  else
    printf '%s\n' "$1"
    id -gn "$1"
  fi
}

create_owned_dir() {
  local path=$1 owner=$2 group=$3 mode=$4
  if [[ -e "$path" && ! -d "$path" ]]; then
    die "路径已存在且不是目录：$path"
  fi
  install -d -m "$mode" "$path"
  if [[ "${NO_SERVICE_USER:-0}" != 1 ]]; then
    chown "$owner:$group" "$path"
  fi
}

load_install_state() {
  local prefix=$1
  if [[ -r "$prefix/install.env" ]]; then
    # shellcheck disable=SC1090
    source "$prefix/install.env"
  fi
}

switch_current() {
  local prefix=$1 version=$2 tmp
  safe_version "$version"
  [[ -d "$prefix/releases/$version" ]] || die "待切换版本不存在：$version"
  if [[ -e "$prefix/current" && ! -L "$prefix/current" ]]; then
    die "current 已存在且不是符号链接，拒绝覆盖"
  fi
  if [[ -L "$prefix/current" ]]; then
    local resolved
    resolved=$(readlink -f "$prefix/current" || true)
    case "$resolved" in
      "$prefix/releases/"*) ;;
      *) die "current 指向安装目录之外，拒绝切换" ;;
    esac
  fi
  tmp="$prefix/.current.$$"
  ln -s "$prefix/releases/$version" "$tmp"
  mv -Tf "$tmp" "$prefix/current"
}

install_release_tree() {
  local root=$1 prefix=$2 version=$3 force=${4:-0}
  local target="$prefix/releases/$version" tmp="$prefix/.release.$$"
  if [[ -d "$target" ]]; then
    if [[ "$force" == 1 ]]; then
      rm -rf -- "$target"
    else
      verify_installed_release "$target"
      [[ "$(read_manifest_field "$target" version)" == "$version" ]] || die "已安装版本清单不一致"
      log "版本 $version 已存在且校验一致，复用现有目录"
      return 0
    fi
  fi
  rm -rf -- "$tmp"
  mkdir -p -- "$tmp"
  cp -a -- "$root/." "$tmp/"
  # 发布目录只含公开程序/静态资源，服务用户必须能够遍历目录并读取文件。
  # 私密配置和数据位于版本目录外，继续使用各自的0700/0600权限。
  chmod -R a+rX -- "$tmp"
  verify_installed_release "$tmp"
  mv -T -- "$tmp" "$target"
}

install_unit_file() {
  local template=$1 destination=$2
  local rendered
  rendered=$(mktemp)
  sed \
    -e "s|@PREFIX@|$PREFIX|g" \
    -e "s|@CONFIG_DIR@|$CONFIG_DIR|g" \
    -e "s|@DATA_DIR@|$DATA_DIR|g" \
    -e "s|@SERVICE_USER@|$SERVICE_USER|g" \
    -e "s|@TIKU_CONFIG_DIR@|$TIKU_CONFIG_DIR|g" \
    -e "s|@TIKU_USER@|$TIKU_USER|g" \
    "$template" >"$rendered"
  install -m 0644 "$rendered" "$destination"
  rm -f -- "$rendered"
}

install_units() {
  [[ "${NO_SYSTEMD:-0}" == 1 ]] && return 0
  require_cmd systemctl
  install -d -m 0755 /etc/systemd/system
  install_unit_file "$SCRIPT_DIR/systemd/byebyeocs.service.in" "/etc/systemd/system/$SERVICE_NAME"
  if [[ "${SKIP_TIKU:-0}" != 1 ]]; then
    install_unit_file "$SCRIPT_DIR/systemd/ocs-tiku.service.in" "/etc/systemd/system/$TIKU_SERVICE_NAME"
  fi
  install_unit_file "$SCRIPT_DIR/systemd/byebyeocs-backup.service.in" "/etc/systemd/system/byebyeocs-backup.service"
  install_unit_file "$SCRIPT_DIR/systemd/byebyeocs-backup.timer.in" "/etc/systemd/system/byebyeocs-backup.timer"
  systemctl daemon-reload
}

unit_is_active() {
  [[ "${NO_SYSTEMD:-0}" == 1 ]] && return 1
  systemctl is-active --quiet "$1" 2>/dev/null
}

restart_active_services() {
  [[ "${NO_RESTART:-0}" == 1 ]] && return 0
  [[ "${NO_SYSTEMD:-0}" == 1 ]] && return 0
  if unit_is_active "$SERVICE_NAME"; then systemctl restart "$SERVICE_NAME"; fi
  if [[ "${SKIP_TIKU:-0}" != 1 ]] && unit_is_active "$TIKU_SERVICE_NAME"; then systemctl restart "$TIKU_SERVICE_NAME"; fi
}

remove_units() {
  [[ "${NO_SYSTEMD:-0}" == 1 ]] && return 0
  for unit in "$SERVICE_NAME" "$TIKU_SERVICE_NAME" "byebyeocs-backup.timer" "byebyeocs-backup.service"; do
    [[ -f "/etc/systemd/system/$unit" ]] || continue
    systemctl disable --now "$unit" >/dev/null 2>&1 || true
    rm -f -- "/etc/systemd/system/$unit"
  done
  systemctl daemon-reload
}
