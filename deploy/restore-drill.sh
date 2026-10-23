#!/usr/bin/env bash
# 隔离恢复演练：只启动临时 initdb 集群，不连接、不写入真实数据库。
set -Eeuo pipefail
umask 077

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

ARCHIVE=""
IDENTITY=""
REPORT=""

usage() {
  cat <<'EOF'
用法：./deploy/restore-drill.sh --archive FILE.age --identity AGE_IDENTITY [--report FILE]

要求：
  - 以非 root 用户运行；PostgreSQL 的 initdb/pg_ctl/createdb/pg_restore/psql 在 PATH 中。
  - 只在临时目录创建独立 initdb 集群和临时数据库，不读取真实数据库，不要求应用账号有 CREATEDB。
  - age 身份私钥只用于本次解密；脚本不会写入生产 backup-status.json。
  - 通过只证明本次在隔离集群中恢复成功；不等于已完成生产恢复演练审批。
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --archive) ARCHIVE=$(normalize_path "$2"); shift 2 ;;
    --identity) IDENTITY=$(normalize_path "$2"); shift 2 ;;
    --report) REPORT=$(normalize_path "$2"); shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数：$1" ;;
  esac
done

[[ -n "$ARCHIVE" && -r "$ARCHIVE" ]] || die "必须提供可读的加密归档 --archive"
[[ -n "$IDENTITY" && -r "$IDENTITY" ]] || die "必须提供可读的 age 身份文件 --identity"
[[ $EUID -ne 0 ]] || die "initdb 不能以 root 运行；请用有权限的普通用户执行隔离演练"
for cmd in age tar initdb pg_ctl createdb pg_restore psql mktemp date sha256sum grep awk head; do require_cmd "$cmd"; done

STARTED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
ARCHIVE_SHA256=$(sha256sum "$ARCHIVE" | awk '{print $1}')
TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/byebyeocs-restore-drill.XXXXXXXX")
SOCKET_DIR="$TMP_ROOT/socket"
PGDATA="$TMP_ROOT/pgdata"
RESTORE_DIR="$TMP_ROOT/restore"
PG_LOG="$TMP_ROOT/postgres.log"
PORT=0
PG_STARTED=0
REPORT_TMP=""

stop_pg() {
  [[ "$PG_STARTED" == 1 ]] || return 0
  if ! pg_ctl -D "$PGDATA" -m immediate -w -t 30 stop >/dev/null 2>&1; then
    local pid
    pid=$(head -n 1 "$PGDATA/postmaster.pid" 2>/dev/null || true)
    # 只在进程命令行确实引用本临时数据目录时补发 SIGKILL，避免误杀无关进程。
    if [[ "$pid" =~ ^[0-9]+$ ]] && grep -Fq -- "$PGDATA" "/proc/$pid/cmdline" 2>/dev/null; then
      kill -9 "$pid" 2>/dev/null || true
    fi
  fi
  PG_STARTED=0
}

cleanup() {
  stop_pg
  if [[ -n "${REPORT_TMP:-}" ]]; then
    rm -f -- "$REPORT_TMP"
  fi
  rm -rf -- "$TMP_ROOT"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$SOCKET_DIR" "$RESTORE_DIR"
age --decrypt --identity "$IDENTITY" --output "$TMP_ROOT/archive.tar" "$ARCHIVE" || die "age 解密失败"

# 拒绝绝对路径、父目录穿越、符号/硬链接、设备/管道/socket 等特殊条目。
while IFS= read -r entry; do
  case "$entry" in
    /*|..|../*|*/../*|*/..) die "归档包含不安全路径：$entry" ;;
  esac
done < <(tar -tf "$TMP_ROOT/archive.tar")
while IFS= read -r entry; do
  type=${entry:0:1}
  case "$type" in
    -|d) ;;
    *) die "归档包含非普通文件/目录条目：${entry}" ;;
  esac
done < <(tar -tvf "$TMP_ROOT/archive.tar")
tar -xf "$TMP_ROOT/archive.tar" --no-same-owner --no-same-permissions -C "$RESTORE_DIR"
[[ -f "$RESTORE_DIR/database/main.dump" ]] || die "归档缺少 database/main.dump"
[[ -f "$RESTORE_DIR/manifest.txt" ]] || die "归档缺少 manifest.txt"
manifest_value() {
  sed -n "s/^$1=//p" "$RESTORE_DIR/manifest.txt" | head -n 1
}
[[ "$(manifest_value format)" == 1 ]] || die "归档 manifest 格式不受支持"
[[ "$(manifest_value main_database)" == true ]] || die "归档 manifest 未标记主数据库备份"
[[ "$(manifest_value config_main)" == true ]] || die "归档 manifest 未标记主配置备份"
if [[ "$(manifest_value tiku_database)" == true && ! -f "$RESTORE_DIR/database/tiku.dump" ]]; then
  die "归档 manifest 声明题库数据库，但缺少 database/tiku.dump，拒绝报告恢复通过"
fi
if [[ "$(manifest_value config_tiku)" == true && ! -d "$RESTORE_DIR/config/tiku" ]]; then
  die "归档 manifest 声明题库配置，但缺少 config/tiku，拒绝报告恢复通过"
fi
CHECKS_FILE="$TMP_ROOT/manifest.sha256"
grep -E '^[0-9a-f]{64}  \./' "$RESTORE_DIR/manifest.txt" >"$CHECKS_FILE" || true
[[ -s "$CHECKS_FILE" ]] || die "归档 manifest 没有可核对的文件摘要"
(cd "$RESTORE_DIR" && sha256sum -c --quiet "$CHECKS_FILE") || die "归档文件与 manifest 摘要不一致"
[[ $(grep -c '  \./database/main.dump$' "$CHECKS_FILE") -ge 1 ]] || die "manifest 未覆盖主数据库 dump"

initdb -D "$PGDATA" --auth=trust --no-locale --encoding=UTF8 >/dev/null
PG_STARTED=1
start_pg() {
  local attempt port
  for attempt in 1 2 3; do
    port=$((20000 + (($$ + attempt * 7919) % 20000)))
    : >"$PG_LOG"
    if pg_ctl -D "$PGDATA" -l "$PG_LOG" -o "-c listen_addresses='' -c unix_socket_directories='$SOCKET_DIR' -c port=$port -c fsync=off -c full_page_writes=off" -w -t 60 start >/dev/null 2>&1; then
      PORT=$port
      return 0
    fi
    # 只对端口/共享内存冲突换端口重试；其他启动失败直接报错，避免无意义重试。
    if ! grep -qE 'pre-existing shared memory block|Address already in use|could not bind' "$PG_LOG"; then
      sed -n '1,20p' "$PG_LOG" >&2
      return 1
    fi
    pg_ctl -D "$PGDATA" -m immediate -w -t 30 stop >/dev/null 2>&1 || true
  done
  return 1
}
if ! start_pg; then
  die "临时 PostgreSQL 启动失败（端口或共享内存冲突重试后仍失败）"
fi
createdb -h "$SOCKET_DIR" -p "$PORT" bbo_restore_main
pg_restore --no-owner --no-privileges --exit-on-error -h "$SOCKET_DIR" -p "$PORT" -d bbo_restore_main "$RESTORE_DIR/database/main.dump"
psql -h "$SOCKET_DIR" -p "$PORT" -d bbo_restore_main -v ON_ERROR_STOP=1 -c 'SELECT 1' >/dev/null

TIKU_RESTORED=false
if [[ -f "$RESTORE_DIR/database/tiku.dump" ]]; then
  createdb -h "$SOCKET_DIR" -p "$PORT" bbo_restore_tiku
  pg_restore --no-owner --no-privileges --exit-on-error -h "$SOCKET_DIR" -p "$PORT" -d bbo_restore_tiku "$RESTORE_DIR/database/tiku.dump"
  psql -h "$SOCKET_DIR" -p "$PORT" -d bbo_restore_tiku -v ON_ERROR_STOP=1 -c 'SELECT 1' >/dev/null
  TIKU_RESTORED=true
fi

COMPLETED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
if [[ -n "$REPORT" ]]; then
  install -d -m 0700 "$(dirname "$REPORT")"
  REPORT_TMP="${REPORT}.tmp.$$"
  printf '{"status":"passed","archive_sha256":"%s","started_at":"%s","completed_at":"%s","method":"isolated-initdb","main_restored":true,"tiku_restored":%s,"production_untouched":true}\n' \
    "$ARCHIVE_SHA256" "$STARTED_AT" "$COMPLETED_AT" "$TIKU_RESTORED" >"$REPORT_TMP"
  chmod 0600 "$REPORT_TMP"
  mv -Tf -- "$REPORT_TMP" "$REPORT"
  REPORT_TMP=""
  log "隔离恢复演练通过，报告：$REPORT"
else
  log "隔离恢复演练通过（未更新生产 backup-status.json）。"
fi
log "这证明当前归档能在临时集群恢复；不等于已完成生产恢复演练审批。"
