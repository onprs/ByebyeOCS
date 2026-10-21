#!/usr/bin/env bash
# 生成 PostgreSQL 一致性逻辑备份，连同配置/私密 keyring 用 age 加密，异机校验后才更新恢复点。
set -Eeuo pipefail
umask 077

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

LOCAL_ONLY=0
require_cmd stat

while [[ $# -gt 0 ]]; do
  case "$1" in
    --local-only) LOCAL_ONLY=1; shift ;;
    -h|--help)
      cat <<'EOF'
用法：sudo ./deploy/backup.sh [--local-only]

读取 /etc/byebyeocs/backup.env 或 BYEBYEOCS_BACKUP_ENV 指定的文件。
默认要求 age 公钥和异机 SSH 目标；只有显式 --local-only 才允许只保留本地加密归档。
本地归档不会被写成 offsite 成功恢复点，也不会更新 operations 读取的 status JSON。
EOF
      exit 0 ;;
    *) die "未知参数：$1" ;;
  esac
done

ENV_FILE=${BYEBYEOCS_BACKUP_ENV:-/etc/byebyeocs/backup.env}
[[ "$ENV_FILE" == /* ]] || die "备份环境文件必须使用绝对路径：$ENV_FILE"
validate_private_file() {
  local path=$1 label=$2 env_file=${3:-0} opened_descriptor=${4:-0} uid mode perm
  [[ -e "$path" || -L "$path" ]] || die "$label 不存在：$path"
  [[ "$opened_descriptor" == 1 || ! -L "$path" ]] || die "$label 不能是符号链接：$path"
  [[ -f "$path" ]] || die "$label 必须是普通文件：$path"
  [[ -r "$path" ]] || die "$label 不可读：$path"
  uid=$(stat -Lc '%u' -- "$path")
  mode=$(stat -Lc '%a' -- "$path")
  perm=$((8#$mode))
  (( (perm & 8#077) == 0 )) || die "$label 不能有组/其他用户读、写或执行权限：$path"
  if [[ "$env_file" == 1 ]]; then
    if [[ $EUID -eq 0 ]]; then
      [[ "$uid" == 0 ]] || die "root 执行的备份环境文件必须由 root 所有：$path"
    else
      [[ "$uid" == "$EUID" || "$uid" == 0 ]] || die "备份环境文件必须由当前用户或 root 所有：$path"
    fi
  fi
}
if [[ -e "$ENV_FILE" || -L "$ENV_FILE" ]]; then
  validate_private_file "$ENV_FILE" "备份环境文件" 1
  # 对实际打开的同一inode复核并读取，避免可写父目录在校验后替换环境文件。
  exec {BACKUP_ENV_DESCRIPTOR}<"$ENV_FILE"
  OPENED_ENV_PATH="/proc/$$/fd/$BACKUP_ENV_DESCRIPTOR"
  validate_private_file "$OPENED_ENV_PATH" "已打开的备份环境文件" 1 1
  # shellcheck disable=SC1090
  source "$OPENED_ENV_PATH"
  exec {BACKUP_ENV_DESCRIPTOR}<&-
elif [[ -n "${BYEBYEOCS_BACKUP_ENV:-}" ]]; then
  die "备份环境文件不存在：$ENV_FILE"
fi

CONFIG_DIR=$(normalize_path "${BYEBYEOCS_CONFIG_DIR:-/etc/byebyeocs}")
DSN_FILE=$(normalize_path "${BYEBYEOCS_DSN_FILE:-/etc/byebyeocs/secrets/database.dsn}")
STATUS_FILE=$(normalize_path "${BYEBYEOCS_BACKUP_STATUS_FILE:-/etc/byebyeocs/secrets/backup-status.json}")
STAGING=$(normalize_path "${BYEBYEOCS_BACKUP_STAGING:-/var/backups/byebyeocs}")
TIKU_CONFIG_DIR=""
TIKU_DSN_FILE=""
[[ -n "${BYEBYEOCS_TIKU_CONFIG_DIR:-}" ]] && TIKU_CONFIG_DIR=$(normalize_path "$BYEBYEOCS_TIKU_CONFIG_DIR")
[[ -n "${BYEBYEOCS_TIKU_DSN_FILE:-}" ]] && TIKU_DSN_FILE=$(normalize_path "$BYEBYEOCS_TIKU_DSN_FILE")
AGE_RECIPIENT=${BYEBYEOCS_AGE_RECIPIENT:-}
OFFSITE_SSH=${BYEBYEOCS_OFFSITE_SSH:-}
OFFSITE_DIR=${BYEBYEOCS_OFFSITE_DIR:-}
STATUS_OWNER=${BYEBYEOCS_BACKUP_STATUS_OWNER:-}

[[ -d "$CONFIG_DIR" ]] || die "配置目录不存在：$CONFIG_DIR"
[[ "$CONFIG_DIR" != "/" && "$STATUS_FILE" != "/" && "$STAGING" != "/" ]] || die "备份路径不能是根目录"
[[ ! -L "$STATUS_FILE" ]] || die "恢复点状态文件不能是符号链接"
validate_private_file "$DSN_FILE" "主数据库 DSN 文件"
[[ -s "$DSN_FILE" ]] || die "主数据库 DSN 文件为空：$DSN_FILE"
if [[ -n "$TIKU_CONFIG_DIR" && -f "$TIKU_CONFIG_DIR/ocs-tiku-fork.json" && -z "$TIKU_DSN_FILE" ]]; then
  die "题库活动配置存在但未设置 BYEBYEOCS_TIKU_DSN_FILE，拒绝生成不完整备份"
fi
[[ -n "$AGE_RECIPIENT" ]] || die "未设置 BYEBYEOCS_AGE_RECIPIENT；不使用未加密的数据库备份"
[[ "$AGE_RECIPIENT" =~ ^age1[0-9a-z]{20,}$ ]] || die "age recipient 格式无效"
if [[ "$LOCAL_ONLY" != 1 ]]; then
  [[ -n "$OFFSITE_SSH" ]] || die "未设置异机 SSH；本地复制不能记为 offsite。仅本地测试请显式 --local-only"
  [[ "$OFFSITE_SSH" =~ ^[A-Za-z0-9._-]+@[A-Za-z0-9.:-]+$ ]] || die "异机 SSH 目标格式无效"
  [[ "$OFFSITE_DIR" =~ ^/[A-Za-z0-9._/-]+$ && "$OFFSITE_DIR" != *"/../"* && "$OFFSITE_DIR" != *".." ]] || die "异机目录必须为安全绝对路径"
fi

for cmd in pg_dump age tar sha256sum find sort xargs flock date mktemp install stat tr python3; do require_cmd "$cmd"; done
SSH_OPTIONS=(-o BatchMode=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=10 -o ServerAliveInterval=15 -o ServerAliveCountMax=2)
if [[ "$LOCAL_ONLY" != 1 ]]; then
  for cmd in scp ssh getent hostname awk; do require_cmd "$cmd"; done
  REMOTE_HOST=${OFFSITE_SSH#*@}
  REMOTE_HOST=${REMOTE_HOST#[}
  REMOTE_HOST=${REMOTE_HOST%]}
  [[ "$REMOTE_HOST" != */* && "$REMOTE_HOST" != *" "* ]] || die "异机主机格式无效"
  case "${REMOTE_HOST,,}" in
    localhost|localhost.*|ip6-localhost|127.*|::1|0.0.0.0|::) die "异机 SSH 目标不能是本机/回环地址" ;;
  esac
  mapfile -t REMOTE_IPS < <(getent ahosts "$REMOTE_HOST" | awk '{print $1}' | sort -u)
  [[ ${#REMOTE_IPS[@]} -gt 0 ]] || die "异机主机无法解析；为避免本机误备份，失败关闭"
  LOCAL_IPS=$(hostname -I 2>/dev/null || true)
  for ip in "${REMOTE_IPS[@]}"; do
    case "$ip" in
      127.*|::1|0.0.0.0|::|fe80:*|169.254.*) die "异机 SSH 目标解析为本机/回环/链路本地地址" ;;
    esac
    for local_ip in $LOCAL_IPS; do
      [[ "$ip" != "$local_ip" ]] || die "异机 SSH 目标解析为本机地址"
    done
  done
  LOCAL_MACHINE_ID=""
  for file in /etc/machine-id /var/lib/dbus/machine-id; do
    if [[ -r "$file" ]]; then LOCAL_MACHINE_ID=$(tr -d '[:space:]' <"$file"); break; fi
  done
  REMOTE_MACHINE_ID=$(ssh "${SSH_OPTIONS[@]}" "$OFFSITE_SSH" 'cat /etc/machine-id 2>/dev/null || cat /var/lib/dbus/machine-id 2>/dev/null' 2>/dev/null | tr -d '[:space:]') || die "无法读取异机 machine-id；失败关闭"
  [[ -n "$REMOTE_MACHINE_ID" ]] || die "异机 machine-id 为空；失败关闭"
  if [[ -n "$LOCAL_MACHINE_ID" && "$REMOTE_MACHINE_ID" == "$LOCAL_MACHINE_ID" ]]; then
    die "异机 SSH 目标与本机 machine-id 相同，不是异机备份"
  fi
fi
if [[ -e "$CONFIG_DIR" ]] && find "$CONFIG_DIR" -type l -print -quit | grep -q .; then
  die "配置目录包含符号链接，拒绝打包"
fi
if [[ -n "$TIKU_CONFIG_DIR" && -d "$TIKU_CONFIG_DIR" ]] && find "$TIKU_CONFIG_DIR" -type l -print -quit | grep -q .; then
  die "题库配置目录包含符号链接，拒绝打包"
fi

prepare_pg_connection() {
  local dsn_file=$1 label=$2 directory service pgpass service_name
  dsn_file=$(normalize_path "$dsn_file")
  directory="$TMP_ROOT/connection-$label"
  service="$directory/service.conf"
  pgpass="$directory/pgpass"
  service_name="byebyeocs_backup_$label"
  [[ ! -e "$directory" ]] || die "临时数据库连接目录已存在"
  mkdir -p -- "$directory"
  chmod 0700 "$directory"
  python3 "$SCRIPT_DIR/pg_connection.py" --dsn-file "$dsn_file" --service-file "$service" \
    --service-name "$service_name" --pgpass-file "$pgpass" || die "数据库 DSN 解析失败"
  [[ -s "$service" && -s "$pgpass" ]] || die "数据库连接临时文件生成失败"
  PG_SERVICE_ENV=(env \
    -u PGDATABASE -u PGHOST -u PGHOSTADDR -u PGPORT -u PGUSER -u PGPASSWORD \
    -u PGSSLMODE -u PGOPTIONS -u PGSERVICE -u PGSERVICEFILE -u PGPASSFILE -u PGAPPNAME \
    PGSERVICEFILE="$service" PGSERVICE="$service_name" PGPASSFILE="$pgpass" PGAPPNAME=byebyeocs-backup)
}

install -d -m 0700 "$STAGING"
LOCK_FILE="$STAGING/.backup.lock"
exec 9>"$LOCK_FILE"
flock -n 9 || die "已有备份在运行"

TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/byebyeocs-backup.XXXXXXXX")
TMP_CONTENT="$TMP_ROOT/content"
ARCHIVE=""
KEEP_ARCHIVE=0
cleanup() {
  rm -rf -- "$TMP_ROOT"
  if [[ -f "${ARCHIVE:-}" && "${KEEP_ARCHIVE:-0}" != 1 ]]; then
    rm -f -- "$ARCHIVE"
  fi
}
trap cleanup EXIT

mkdir -p "$TMP_CONTENT/database" "$TMP_CONTENT/config"
DUMP_STARTED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# pg_dump 自身提供一致性快照；两个数据库分别生成各自一致快照，不能跨库宣称单一原子快照。
prepare_pg_connection "$DSN_FILE" main
"${PG_SERVICE_ENV[@]}" pg_dump --format=custom --no-password --file="$TMP_CONTENT/database/main.dump" || die "主数据库 pg_dump 失败"
TIKU_DUMPED=0
if [[ -n "$TIKU_DSN_FILE" ]]; then
  validate_private_file "$TIKU_DSN_FILE" "题库数据库 DSN 文件"
  [[ -s "$TIKU_DSN_FILE" ]] || die "题库数据库 DSN 文件为空：$TIKU_DSN_FILE"
  prepare_pg_connection "$TIKU_DSN_FILE" tiku
  "${PG_SERVICE_ENV[@]}" pg_dump --format=custom --no-password --file="$TMP_CONTENT/database/tiku.dump" || die "题库数据库 pg_dump 失败"
  TIKU_DUMPED=1
fi

mkdir -p "$TMP_CONTENT/config/main"
cp -a -- "$CONFIG_DIR/." "$TMP_CONTENT/config/main/"
if [[ -n "$TIKU_CONFIG_DIR" && -d "$TIKU_CONFIG_DIR" ]]; then
  mkdir -p "$TMP_CONTENT/config/tiku"
  cp -a -- "$TIKU_CONFIG_DIR/." "$TMP_CONTENT/config/tiku/"
fi

cat >"$TMP_CONTENT/manifest.txt" <<EOF
format=1
dump_started_at=$DUMP_STARTED_AT
main_database=true
tiku_database=$([[ "$TIKU_DUMPED" == 1 ]] && echo true || echo false)
config_main=true
config_tiku=$([[ -d "$TMP_CONTENT/config/tiku" ]] && echo true || echo false)
EOF
(
  cd "$TMP_CONTENT"
  find . -type f ! -name manifest.txt -print0 | sort -z | xargs -0 sha256sum
) >>"$TMP_CONTENT/manifest.txt"

ARCHIVE="$STAGING/byebyeocs-backup-${DUMP_STARTED_AT//:/-}-$$.tar.age"
tar -C "$TMP_CONTENT" -cf - . | age --recipient "$AGE_RECIPIENT" --output "$ARCHIVE" || die "age 加密失败"
[[ -s "$ARCHIVE" ]] || die "备份归档为空"
ARCHIVE_SHA256=$(sha256sum "$ARCHIVE" | awk '{print $1}')

if [[ "$LOCAL_ONLY" == 1 ]]; then
  LOCAL_RETAIN_COUNT=${BYEBYEOCS_LOCAL_RETAIN_COUNT:-3}
  LOCAL_MAX_BYTES=${BYEBYEOCS_LOCAL_MAX_BYTES:-2147483648}
  [[ "$LOCAL_RETAIN_COUNT" =~ ^[1-9][0-9]?$ ]] || die "BYEBYEOCS_LOCAL_RETAIN_COUNT 必须是 1 到 99"
  [[ "$LOCAL_MAX_BYTES" =~ ^[1-9][0-9]*$ ]] || die "BYEBYEOCS_LOCAL_MAX_BYTES 必须为正整数"
  [[ $(stat -c '%s' -- "$ARCHIVE") -le "$LOCAL_MAX_BYTES" ]] || { rm -f -- "$ARCHIVE"; die "本地归档超过本地预算；未更新成功状态"; }
  KEEP_ARCHIVE=1
  mapfile -t LOCAL_ARCHIVES < <(ls -1t "$STAGING"/byebyeocs-backup-*.tar.age 2>/dev/null || true)
  for ((i = LOCAL_RETAIN_COUNT; i < ${#LOCAL_ARCHIVES[@]}; i++)); do
    rm -f -- "${LOCAL_ARCHIVES[$i]}"
  done
  log "已生成本地加密归档：$ARCHIVE"
  log "本地归档不是异机备份；未更新 operations 成功恢复点，offsite=false。"
  log "本地保留最近 $LOCAL_RETAIN_COUNT 份、单份上限 $LOCAL_MAX_BYTES 字节。"
  log "archive_sha256=$ARCHIVE_SHA256"
  exit 0
fi

REMOTE_PATH="${OFFSITE_DIR%/}/$(basename "$ARCHIVE")"
scp "${SSH_OPTIONS[@]}" -q -p "$ARCHIVE" "$OFFSITE_SSH:$OFFSITE_DIR/" || die "异机上传失败；未更新恢复点状态"
REMOTE_SHA256=$(ssh "${SSH_OPTIONS[@]}" "$OFFSITE_SSH" "sha256sum -- '$REMOTE_PATH'" | awk 'NF {value=$1} END {print value}')
[[ "$REMOTE_SHA256" == "$ARCHIVE_SHA256" ]] || die "异机 SHA-256 校验不一致；未更新恢复点状态"

VERIFIED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
install -d -m 0700 "$(dirname "$STATUS_FILE")"
STATUS_TMP="${STATUS_FILE}.tmp.$$"
printf '{"status":"ok","last_recoverable_at":"%s","verified_at":"%s","offsite":true,"archive_sha256":"%s","restore_drill_verified":false}\n' \
  "$DUMP_STARTED_AT" "$VERIFIED_AT" "$ARCHIVE_SHA256" >"$STATUS_TMP"
chmod 0600 "$STATUS_TMP"
if [[ -n "$STATUS_OWNER" && $EUID -eq 0 ]]; then
  STATUS_GROUP=$(id -gn "$STATUS_OWNER" 2>/dev/null || true)
  [[ -n "$STATUS_GROUP" ]] || { rm -f "$STATUS_TMP"; die "恢复点状态所有者用户不存在"; }
  chown "$STATUS_OWNER:$STATUS_GROUP" "$STATUS_TMP" || { rm -f "$STATUS_TMP"; die "无法设置恢复点状态文件所有者"; }
fi
mv -Tf -- "$STATUS_TMP" "$STATUS_FILE" || die "无法原子更新恢复点状态"

KEEP_ARCHIVE=0
log "备份完成：$REMOTE_PATH"
log "远端校验通过；last_recoverable_at=$DUMP_STARTED_AT（dump 开始保守值），verified_at=$VERIFIED_AT"
log "archive_sha256=$ARCHIVE_SHA256"
log "status=ok 只表示异机校验通过；restore_drill_verified=false，恢复演练需单独执行。"
