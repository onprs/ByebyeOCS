#!/usr/bin/env bash
# 隔离测试部署与备份脚本。只用临时目录和假命令，不连接真实 SSH/PostgreSQL。
set -Eeuo pipefail
umask 077

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
for script in "$ROOT"/deploy/*.sh "$ROOT"/scripts/*.sh; do bash -n "$script"; done
TMP=$(mktemp -d "${TMPDIR:-/tmp}/byebyeocs-deploy-test.XXXXXXXX")
trap 'rm -rf -- "$TMP"' EXIT
PASS=0

ok() { PASS=$((PASS + 1)); printf 'ok %s - %s\n' "$PASS" "$1"; }
fail() { printf 'not ok %s - %s\n' "$((PASS + 1))" "$1" >&2; exit 1; }
expect_fail() {
  local name=$1; shift
  if "$@" >/dev/null 2>&1; then fail "$name（预期失败但成功）"; fi
  ok "$name"
}

# 在独立会话中执行命令并写入真实 PID，便于测试向整个进程组发送信号。
start_new_session() {
  local pidfile=$1; shift
  setsid bash -c '
    pidfile=$1
    shift
    printf "%s\n" "$$" >"$pidfile"
    exec "$@"
  ' _ "$pidfile" "$@" &
  # 不让测试 shell 在信号杀死后台任务时输出作业通知；进程仍继续运行。
  disown $! 2>/dev/null || true
}
wait_for_file() {
  local path=$1 i
  for ((i = 0; i < 200; i++)); do
    [[ -e "$path" ]] && return 0
    sleep 0.05
  done
  return 1
}

make_release() {
  local version=$1 dir=$2
  mkdir -p "$dir/bin" "$dir/apps/admin" "$dir/apps/client-web" "$dir/configs" "$dir/deploy"
  cp -a "$ROOT/deploy/." "$dir/deploy/"
  cp -a "$ROOT/configs/." "$dir/configs/"
  printf '#!/usr/bin/env bash\nprintf "fake-server-called\\n" >>"${FAKE_CALL_LOG:-/dev/null}"\n' >"$dir/bin/byebyeocs"
  printf '#!/usr/bin/env bash\nprintf "fake-tiku-called\\n" >>"${FAKE_CALL_LOG:-/dev/null}"\n' >"$dir/bin/ocs-tiku-fork"
  chmod 0755 "$dir/bin/byebyeocs" "$dir/bin/ocs-tiku-fork"
  printf '<html>admin</html>\n' >"$dir/apps/admin/index.html"
  printf '<html>client</html>\n' >"$dir/apps/client-web/index.html"
  printf '{"format":1,"version":"%s","goos":"linux","goarch":"amd64","commit":"test","build_time":"2026-01-01T00:00:00Z","main_schema":7,"tiku_schema":1,"project_license":"not_declared"}\n' "$version" >"$dir/release.json"
  (cd "$dir" && find . -type f ! -name checksums.txt -print0 | sort -z | xargs -0 sha256sum >checksums.txt)
}

# 安装需要清单和 checksums；项目许可证未声明时也不强制根 LICENSE。
V1="$TMP/v1"
V2="$TMP/v2"
make_release 1.0.0 "$V1"
make_release 1.1.0 "$V2"

PREFIX="$TMP/opt/byebyeocs"
CONFIG="$TMP/etc/byebyeocs"
DATA="$TMP/var/lib/byebyeocs"
TIKU_CONFIG="$TMP/etc/ocs-tiku"
mkdir -p "$CONFIG/secrets" "$DATA"
printf 'KEEP-CONFIG\n' >"$CONFIG/config.yaml"
printf 'KEEP-KEY\n' >"$CONFIG/secrets/application.key"
printf 'KEEP-INDEX\n' >"$CONFIG/secrets/index.key"
printf 'KEEP-DATA\n' >"$DATA/sentinel"

"$V1/deploy/install.sh" --prefix "$PREFIX" --config-dir "$CONFIG" --data-dir "$DATA" \
  --tiku-config-dir "$TIKU_CONFIG" --no-systemd --no-service-user >"$TMP/install.log"
[[ -L "$PREFIX/current" && "$(readlink -f "$PREFIX/current")" == "$PREFIX/releases/1.0.0" ]] || fail "install current"
[[ "$(cat "$CONFIG/config.yaml")" == "KEEP-CONFIG" ]] || fail "install 不得覆盖现有 config"
[[ "$(cat "$CONFIG/secrets/application.key")" == "KEEP-KEY" ]] || fail "install 不得覆盖现有 key"
[[ "$(cat "$DATA/sentinel")" == "KEEP-DATA" ]] || fail "install 不得改写数据"
[[ -f "$CONFIG/config.yaml.example" && -f "$TIKU_CONFIG/ocs-tiku-fork.json.example" ]] || fail "install 示例配置"
ok "安装保留现有配置/密钥/数据并生成示例"

[[ -z "$(find "$PREFIX/releases/1.0.0" -type d ! -perm -005 -print -quit)" ]] || fail "专用服务用户无法遍历版本目录"
[[ -z "$(find "$PREFIX/releases/1.0.0" -type f ! -perm -004 -print -quit)" ]] || fail "专用服务用户无法读取程序/静态资源"
[[ "$(stat -c %a "$CONFIG")" == 700 && "$(stat -c %a "$DATA")" == 700 && "$(stat -c %a "$CONFIG/secrets/application.key")" == 600 ]] || fail "发布权限修复不得放宽私有配置和数据"
ok "公开程序可被服务用户读取且配置/密钥/数据保持私有"

"$V1/deploy/install.sh" --prefix "$PREFIX" --config-dir "$CONFIG" --data-dir "$DATA" \
  --tiku-config-dir "$TIKU_CONFIG" --no-systemd --no-service-user >/dev/null
[[ "$(cat "$CONFIG/config.yaml")" == "KEEP-CONFIG" && -L "$PREFIX/current" ]] || fail "重复安装幂等"
ok "重复安装幂等"

"$V2/deploy/upgrade.sh" --prefix "$PREFIX" --no-systemd --no-restart >"$TMP/upgrade.log"
[[ "$(readlink -f "$PREFIX/current")" == "$PREFIX/releases/1.1.0" ]] || fail "upgrade 切换版本"
[[ "$(cat "$CONFIG/config.yaml")" == "KEEP-CONFIG" && "$(cat "$DATA/sentinel")" == "KEEP-DATA" ]] || fail "upgrade 改动配置/数据"
ok "升级切换版本且不改配置/数据"

"$V2/deploy/rollback.sh" --prefix "$PREFIX" --version 1.0.0 --no-systemd --no-restart >"$TMP/rollback.log"
[[ "$(readlink -f "$PREFIX/current")" == "$PREFIX/releases/1.0.0" ]] || fail "rollback 切换版本"
ok "回滚切换到已安装版本且不执行数据库迁移"

# 状态文件缺失时不得按默认路径猜配置或切换版本。
cp "$PREFIX/install.env" "$TMP/install.env.keep"
rm -f "$PREFIX/install.env"
current_before=$(readlink -f "$PREFIX/current")
expect_fail "缺少安装状态时拒绝升级" "$V2/deploy/upgrade.sh" --prefix "$PREFIX" --no-systemd --no-restart
[[ "$(readlink -f "$PREFIX/current")" == "$current_before" ]] || fail "缺少状态时仍切换了 current"
mv "$TMP/install.env.keep" "$PREFIX/install.env"
ok "缺少安装状态时升级不切换版本"

"$V1/deploy/uninstall.sh" --prefix "$PREFIX" --config-dir "$CONFIG" --data-dir "$DATA" \
  --tiku-config-dir "$TIKU_CONFIG" --no-systemd >"$TMP/uninstall.log"
[[ ! -e "$PREFIX/releases" && ! -e "$PREFIX/current" ]] || fail "uninstall 移除程序"
[[ "$(cat "$CONFIG/config.yaml")" == "KEEP-CONFIG" && "$(cat "$DATA/sentinel")" == "KEEP-DATA" ]] || fail "uninstall 保留数据"
ok "卸载保留配置/密钥/数据"

expect_fail "拒绝根目录前缀" "$V1/deploy/install.sh" --prefix / --no-systemd --no-service-user
expect_fail "拒绝根目录配置" "$V1/deploy/install.sh" --prefix "$TMP/p2" --config-dir / --no-systemd --no-service-user
expect_fail "拒绝过宽的程序前缀" "$V1/deploy/install.sh" --prefix /etc --no-systemd --no-service-user
expect_fail "拒绝共享系统数据目录" "$V1/deploy/install.sh" --prefix "$TMP/p3" --data-dir /var/lib --no-systemd --no-service-user
expect_fail "拒绝 root 服务用户" "$V1/deploy/install.sh" --prefix "$TMP/p4" --service-user root --no-systemd --no-service-user

# ---------- 卸载的破坏性路径保护：用假 rm 证明校验发生在删除之前 ----------
PROTECTBIN="$TMP/protectbin"
mkdir -p "$PROTECTBIN"
cat >"$PROTECTBIN/rm" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${FAKE_RM_LOG:?}"
exit 0
EOF
chmod 0755 "$PROTECTBIN/rm"
FAKE_RM_LOG="$TMP/protect-rm.log"
: >"$FAKE_RM_LOG"
expect_fail "拒绝 --purge 系统配置目录" env PATH="$PROTECTBIN:$PATH" FAKE_RM_LOG="$FAKE_RM_LOG" \
  "$V1/deploy/uninstall.sh" --prefix "$PREFIX" --config-dir /etc --data-dir "$DATA" \
  --tiku-config-dir "$TIKU_CONFIG" --purge --yes --no-systemd
[[ ! -s "$FAKE_RM_LOG" ]] || fail "--purge 在路径校验前执行了 rm"
ok "--purge 拒绝系统目录且不执行删除"

EMPTY_PREFIX="$TMP/empty-prefix"
mkdir -p "$EMPTY_PREFIX"
: >"$FAKE_RM_LOG"
env PATH="$PROTECTBIN:$PATH" FAKE_RM_LOG="$FAKE_RM_LOG" "$V1/deploy/uninstall.sh" \
  --prefix "$EMPTY_PREFIX" --no-systemd >/dev/null
[[ ! -s "$FAKE_RM_LOG" ]] || fail "无安装记录的非 purge 卸载执行了 rm"
ok "无安装记录的非 purge 卸载安全无操作"

: >"$FAKE_RM_LOG"
expect_fail "无安装记录时拒绝自定义 purge 路径" env PATH="$PROTECTBIN:$PATH" FAKE_RM_LOG="$FAKE_RM_LOG" \
  "$V1/deploy/uninstall.sh" --prefix "$EMPTY_PREFIX" --config-dir "$TMP/custom-etc" \
  --data-dir "$TMP/custom-data" --tiku-config-dir "$TMP/custom-tiku" --purge --yes --no-systemd
[[ ! -s "$FAKE_RM_LOG" ]] || fail "无安装记录的自定义 purge 执行了 rm"
ok "无安装记录时自定义 purge 不执行删除"

# 无安装记录但使用默认路径时，仍允许清理产品默认目录（假 rm 仅记录）。
: >"$FAKE_RM_LOG"
env PATH="$PROTECTBIN:$PATH" FAKE_RM_LOG="$FAKE_RM_LOG" "$V1/deploy/uninstall.sh" \
  --prefix "$EMPTY_PREFIX" --purge --yes --no-systemd >/dev/null
grep -q -- "/etc/byebyeocs" "$FAKE_RM_LOG" || fail "无安装记录的 --purge 未按默认路径清理"
ok "无安装记录的 --purge 只清理默认路径"

# 有安装记录时，--purge 按记录的路径删除；不靠命令行猜测自定义目录。
PURGE_ROOT="$TMP/purge"
"$V1/deploy/install.sh" --prefix "$PURGE_ROOT/opt" --config-dir "$PURGE_ROOT/etc" \
  --data-dir "$PURGE_ROOT/data" --tiku-config-dir "$PURGE_ROOT/tiku" \
  --no-systemd --no-service-user >/dev/null
"$V1/deploy/uninstall.sh" --prefix "$PURGE_ROOT/opt" --purge --yes --no-systemd >/dev/null
[[ ! -e "$PURGE_ROOT/etc" && ! -e "$PURGE_ROOT/data" && ! -e "$PURGE_ROOT/tiku" && ! -e "$PURGE_ROOT/opt" ]] || fail "--purge 未按安装记录删除目录"
ok "显式 --purge 按安装记录删除配置/数据目录"

# ---------- 并发安装锁 ----------
mkdir -p "$PREFIX"
mkfifo "$TMP/install-lock.pipe"
(
  exec 8>"$PREFIX/.deploy.lock"
  flock -n 8
  : >"$TMP/install-lock-ready"
  read -r _ <"$TMP/install-lock.pipe"
) &
LOCK_HOLDER=$!
wait_for_file "$TMP/install-lock-ready" || fail "未能获取安装锁测试句柄"
expect_fail "并发安装被锁拒绝" "$V1/deploy/install.sh" --prefix "$PREFIX" --config-dir "$CONFIG" \
  --data-dir "$DATA" --tiku-config-dir "$TIKU_CONFIG" --no-systemd --no-service-user
printf '\n' >"$TMP/install-lock.pipe"
wait "$LOCK_HOLDER" 2>/dev/null || true
rm -f "$TMP/install-lock.pipe"

"$V1/deploy/install.sh" --prefix "$PREFIX" --config-dir "$CONFIG" --data-dir "$DATA" \
  --tiku-config-dir "$TIKU_CONFIG" --no-systemd --no-service-user >/dev/null

# ---------- 强制失败/中断后的原子性与清理 ----------
FAILBIN="$TMP/failbin"
mkdir -p "$FAILBIN"
cat >"$FAILBIN/cp" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod 0755 "$FAILBIN/cp"
expect_fail "强制重装失败保留旧版本" env PATH="$FAILBIN:$PATH" "$V1/deploy/install.sh" \
  --prefix "$PREFIX" --config-dir "$CONFIG" --data-dir "$DATA" --tiku-config-dir "$TIKU_CONFIG" \
  --no-systemd --no-service-user --force
[[ "$(readlink -f "$PREFIX/current")" == "$PREFIX/releases/1.0.0" ]] || fail "强制重装失败后 current 悬空"
(cd "$PREFIX/current" && sha256sum -c --quiet checksums.txt) || fail "强制重装失败损坏当前版本"
[[ -z "$(find "$PREFIX" -maxdepth 1 \( -name '.release.*' -o -name '.replaced.*' \) -print -quit)" ]] || fail "强制重装失败留下临时目录"
ok "强制重装失败保留当前版本并清理临时目录"

mv "$PREFIX/releases/1.0.0" "$PREFIX/.replaced.1.0.0"
"$V1/deploy/install.sh" --prefix "$PREFIX" --config-dir "$CONFIG" --data-dir "$DATA" \
  --tiku-config-dir "$TIKU_CONFIG" --no-systemd --no-service-user >/dev/null
[[ "$(readlink -f "$PREFIX/current")" == "$PREFIX/releases/1.0.0" ]] || fail "中断替换未恢复版本目录"
[[ ! -e "$PREFIX/.replaced.1.0.0" ]] || fail "恢复后残留 .replaced"
ok "自动恢复中断的强制替换"

mkdir -p "$PREFIX/.release.stale"
printf 'stale\n' >"$PREFIX/.install.env.stale"
ln -s /nonexistent "$PREFIX/.current.stale"
"$V1/deploy/install.sh" --prefix "$PREFIX" --config-dir "$CONFIG" --data-dir "$DATA" \
  --tiku-config-dir "$TIKU_CONFIG" --no-systemd --no-service-user >/dev/null
[[ ! -e "$PREFIX/.release.stale" && ! -e "$PREFIX/.install.env.stale" && ! -L "$PREFIX/.current.stale" ]] || fail "安装未清理中断残留"
ok "安装启动清理被中断的临时项"

# ---------- libpq DSN 解析器的安全/多主机边界（不连接数据库） ----------
PG_CONN_DIR="$TMP/pgconn"
mkdir -p "$PG_CONN_DIR"
printf 'host=host1,host2 port=5432,5433 dbname=db user=u password=p\n' >"$PG_CONN_DIR/multi.dsn"
python3 "$ROOT/deploy/pg_connection.py" --dsn-file "$PG_CONN_DIR/multi.dsn" \
  --service-file "$PG_CONN_DIR/service" --service-name test --pgpass-file "$PG_CONN_DIR/pgpass"
[[ $(wc -l <"$PG_CONN_DIR/pgpass") -eq 2 ]] || fail "多 host 未生成逐 host pgpass"
grep -q '^host1:5432:db:u:p$' "$PG_CONN_DIR/pgpass" || fail "pgpass 第一 host 不符"
grep -q '^host2:5433:db:u:p$' "$PG_CONN_DIR/pgpass" || fail "pgpass 第二 host 不符"
grep -q '^password=' "$PG_CONN_DIR/service" && fail "service file 泄露密码"
ok "DSN 多 host 生成逐 host pgpass 且 service 不含密码"

printf 'host=h dbname=db user=u passfile=/tmp/private\n' >"$PG_CONN_DIR/unsupported.dsn"
expect_fail "拒绝 passfile DSN" python3 "$ROOT/deploy/pg_connection.py" \
  --dsn-file "$PG_CONN_DIR/unsupported.dsn" --service-file "$PG_CONN_DIR/s2" \
  --service-name test2 --pgpass-file "$PG_CONN_DIR/p2"
if [[ -e "$PG_CONN_DIR/s2" || -e "$PG_CONN_DIR/p2" ]]; then fail "拒绝参数后仍生成连接文件"; fi

printf "host='BROKEN SECRET\n" >"$PG_CONN_DIR/bad.dsn"
python3 "$ROOT/deploy/pg_connection.py" --dsn-file "$PG_CONN_DIR/bad.dsn" \
  --service-file "$PG_CONN_DIR/s3" --service-name test3 --pgpass-file "$PG_CONN_DIR/p3" \
  >"$PG_CONN_DIR/bad.out" 2>"$PG_CONN_DIR/bad.err" && fail "非法 DSN 应失败"
grep -q 'BROKEN SECRET' "$PG_CONN_DIR/bad.out" "$PG_CONN_DIR/bad.err" && fail "错误输出泄露 DSN 片段"
ok "DSN 解析失败不输出原始 DSN/parser 消息"

# ---------- 假 pg_dump / age / scp / ssh，验证备份成功、失败和 local-only ----------
FAKEBIN="$TMP/fakebin"
mkdir -p "$FAKEBIN"
cat >"$FAKEBIN/pg_dump" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
file=""; while [[ $# -gt 0 ]]; do case "$1" in --file=*) file=${1#--file=};; esac; shift; done
[[ -n "$file" ]] || exit 9
printf 'PGDATABASE=%s\n' "${PGDATABASE:-}" >"${FAKE_PG_LOG:-/dev/null}"
printf 'fake-dump\n' >"$file"
EOF
cat >"$FAKEBIN/age" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
out=""; decrypt=0; input=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) out=$2; shift;;
    --decrypt) decrypt=1;;
    --identity) shift;;
    *) input=$1;;
  esac
  shift
done
[[ -n "$out" ]] || exit 9
if [[ "$decrypt" == 1 ]]; then
  cp -- "$input" "$out"
elif [[ -n "${FAKE_AGE_HANG:-}" ]]; then
  printf 'partial-age\n' >"$out"
  : >"${FAKE_AGE_READY:?}"
  sleep 300
else
  cat >"$out"
fi
EOF
cat >"$FAKEBIN/scp" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
src=${@: -2:1}; dst=${@: -1}
target=${dst#*:}
name=$(basename -- "$target")
mkdir -p "$FAKE_REMOTE_DIR"
if [[ "${FAKE_SCP_PARTIAL:-0}" == 1 ]]; then
  printf 'partial-upload\n' >"$FAKE_REMOTE_DIR/$name"
  exit 1
fi
cp -- "$src" "$FAKE_REMOTE_DIR/$name"
EOF
cat >"$FAKEBIN/ssh" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
cmd=${@: -1}
if [[ "$cmd" == *machine-id* ]]; then
  printf '%s\n' "${FAKE_REMOTE_MACHINE_ID:-remote-machine-id}"
  exit 0
fi
if [[ "$cmd" == "rm -f -- "* ]]; then
  [[ "${FAKE_SSH_RM_FAIL:-0}" == 1 ]] && exit 9
  path=$(printf '%s' "$cmd" | sed -n "s/^rm -f -- '\([^']*\)'$/\1/p")
  [[ -n "$path" ]] || exit 9
  rm -f -- "$FAKE_REMOTE_DIR/$(basename "$path")"
  exit 0
fi
if [[ "$cmd" =~ ^mv\ -f\ --\ \'([^\']*)\'\ \'([^\']*)\'$ ]]; then
  [[ "${FAKE_SSH_MV_FAIL:-0}" == 1 ]] && exit 9
  src=${BASH_REMATCH[1]}; dst=${BASH_REMATCH[2]}
  mv -f -- "$FAKE_REMOTE_DIR/$(basename "$src")" "$FAKE_REMOTE_DIR/$(basename "$dst")"
  exit 0
fi
path=$(printf '%s' "$cmd" | sed -n "s/.*sha256sum -- '\([^']*\)'.*/\1/p")
[[ -n "$path" ]] || exit 9
name=$(basename "$path")
if [[ "${FAKE_SSH_SHA_FAIL:-0}" == 1 ]]; then exit 9; fi
if [[ "${FAKE_SSH_BAD:-0}" == 1 ]]; then printf '%064d  %s\n' 0 "$name"; exit 0; fi
sha256sum "$FAKE_REMOTE_DIR/$name"
EOF
cat >"$FAKEBIN/getent" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
if [[ "${1:-}" == ahosts && -n "${FAKE_REMOTE_IP:-}" ]]; then
  printf '%s  STREAM\n' "$FAKE_REMOTE_IP"
  exit 0
fi
exec /usr/bin/getent "$@"
EOF
chmod 0755 "$FAKEBIN"/*
export FAKE_REMOTE_IP="203.0.113.10"
export FAKE_REMOTE_MACHINE_ID="remote-machine-id"
export PATH="$FAKEBIN:$PATH"
export FAKE_REMOTE_DIR="$TMP/remote"
export FAKE_PG_LOG="$TMP/pg.log"
AGE_RECIPIENT="age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
BACKUPCONFIG="$TMP/backup-config"
BACKUPDSN="$TMP/main.dsn"
TIKU_DSN="$TMP/tiku.dsn"
STATUS="$TMP/status/backup-status.json"
mkdir -p "$BACKUPCONFIG/secrets/keyring"
printf 'fake identity
' >"$TMP/identity"
printf '\n' >"$TMP/backup.env"
printf 'postgres://user:pass@127.0.0.1/db?sslmode=disable\n' >"$BACKUPDSN"
printf 'postgres://user:pass@127.0.0.1/tiku?sslmode=disable\n' >"$TIKU_DSN"
printf 'server-yaml\n' >"$BACKUPCONFIG/config.yaml"
printf 'app-key\n' >"$BACKUPCONFIG/secrets/application.key"
printf 'index-key\n' >"$BACKUPCONFIG/secrets/index.key"
printf 'keyring-v1\n' >"$BACKUPCONFIG/secrets/keyring/1"
TIKU_BACKUP_CONFIG="$TMP/backup-tiku"
mkdir -p "$TIKU_BACKUP_CONFIG/secrets"
printf '{"listen":"127.0.0.1:8092"}\n' >"$TIKU_BACKUP_CONFIG/ocs-tiku-fork.json"
printf 'tiku-dsn\n' >"$TIKU_BACKUP_CONFIG/secrets/database.dsn"

backup_env=(
  BYEBYEOCS_CONFIG_DIR="$BACKUPCONFIG" BYEBYEOCS_DSN_FILE="$BACKUPDSN"
  BYEBYEOCS_TIKU_CONFIG_DIR="$TIKU_BACKUP_CONFIG" BYEBYEOCS_TIKU_DSN_FILE="$TIKU_DSN"
  BYEBYEOCS_BACKUP_STATUS_FILE="$STATUS" BYEBYEOCS_BACKUP_STAGING="$TMP/staging"
  BYEBYEOCS_AGE_RECIPIENT="$AGE_RECIPIENT" BYEBYEOCS_BACKUP_STATUS_OWNER=""
  BYEBYEOCS_OFFSITE_SSH="backup@example.test" BYEBYEOCS_OFFSITE_DIR="/srv/backups"
  BYEBYEOCS_BACKUP_ENV="$TMP/backup.env"
)
env "${backup_env[@]}" "$ROOT/deploy/backup.sh" >"$TMP/backup.log"
[[ -f "$STATUS" ]] || fail "备份成功状态未写入"
python3 - "$STATUS" "$TMP/remote" <<'PY'
import hashlib,json,glob,sys
status=json.load(open(sys.argv[1]))
assert status["status"]=="ok" and status["offsite"] is True
assert status["last_recoverable_at"] and status["verified_at"]
assert status["restore_drill_verified"] is False
files=glob.glob(sys.argv[2]+"/*.age")
assert files, "没有异机归档"
assert hashlib.sha256(open(files[0], "rb").read()).hexdigest()==status["archive_sha256"], "rename 后正式归档与状态摘要不一致"
PY
ok "备份成功只在异机校验后写 ok/offsite 状态"
[[ -z "$(find "$TMP/staging" -maxdepth 1 -type d -name '.work.*' -print -quit)" ]] || fail "正常备份后遗留 staging 明文工作目录"
[[ -z "$(find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age.partial' -print -quit)" ]] || fail "成功备份遗留远端 .partial"
ok "正常备份清理明文工作目录，远端只有正式归档"

ARCHIVE=$(ls "$TMP"/remote/*.age | head -1)
tar -tf "$ARCHIVE" | grep -q 'config/main/secrets/keyring/1' || fail "备份缺少 keyring"
tar -tf "$ARCHIVE" | grep -q 'config/main/secrets/application.key' || fail "备份缺少主密钥"
tar -tf "$ARCHIVE" | grep -q 'database/tiku.dump' || fail "备份缺少题库 dump"
ok "加密归档包含配置、密钥、keyring 和两个数据库 dump"

# 备份环境文件/DSN 权限与本机目标检查：不写状态、不产生完整归档。
cp "$TMP/backup.env" "$TMP/backup.env.good"
chmod 0666 "$TMP/backup.env"
expect_fail "拒绝其他用户可写的备份环境文件" env "${backup_env[@]}" "$ROOT/deploy/backup.sh"
chmod 0600 "$TMP/backup.env"
ln -sf "$TMP/backup.env" "$TMP/backup.env.link"
expect_fail "拒绝符号链接备份环境文件" env "${backup_env[@]}" BYEBYEOCS_BACKUP_ENV="$TMP/backup.env.link" "$ROOT/deploy/backup.sh"
chmod 0666 "$BACKUPDSN"
expect_fail "拒绝非私有 DSN 权限" env "${backup_env[@]}" "$ROOT/deploy/backup.sh"
chmod 0600 "$BACKUPDSN"
local_id=$(cat /etc/machine-id 2>/dev/null | tr -d '[:space:]' || true)
if [[ -n "$local_id" ]]; then
  cp "$STATUS" "$TMP/status.samehost.before"
  expect_fail "拒绝与本机 machine-id 相同的 SSH 目标" env "${backup_env[@]}" FAKE_REMOTE_MACHINE_ID="$local_id" "$ROOT/deploy/backup.sh"
  cmp -s "$STATUS" "$TMP/status.samehost.before" || fail "同机目标改变了成功状态"
fi
expect_fail "拒绝 localhost SSH 目标" env "${backup_env[@]}" BYEBYEOCS_OFFSITE_SSH="backup@localhost" "$ROOT/deploy/backup.sh"

# 失败不能覆盖旧的 ok 状态，也不能在本地/远端留下本次归档。
cp "$STATUS" "$TMP/status.before"
STAGING_BEFORE=$(find "$TMP/staging" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' 2>/dev/null | wc -l | tr -d ' ')
REMOTE_BEFORE=$(find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' 2>/dev/null | wc -l | tr -d ' ')
env "${backup_env[@]}" FAKE_SSH_BAD=1 "$ROOT/deploy/backup.sh" >/dev/null 2>"$TMP/backup-fail.log" || true
cmp -s "$STATUS" "$TMP/status.before" || fail "远端校验失败却更新了成功状态"
STAGING_AFTER=$(find "$TMP/staging" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' 2>/dev/null | wc -l | tr -d ' ')
[[ "$STAGING_BEFORE" == "$STAGING_AFTER" ]] || fail "远端失败留下本次完整归档"
REMOTE_AFTER=$(find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' 2>/dev/null | wc -l | tr -d ' ')
[[ "$REMOTE_BEFORE" == "$REMOTE_AFTER" ]] || fail "远端校验失败留下未验证归档"
ok "异机校验失败不更新状态且清理本机和异机归档"

# 远端摘要读取本身失败（SSH/命令错误）也必须清理 .partial、不更新状态。
REMOTE_BEFORE=$(find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' 2>/dev/null | wc -l | tr -d ' ')
env "${backup_env[@]}" FAKE_SSH_SHA_FAIL=1 "$ROOT/deploy/backup.sh" >/dev/null 2>&1 || true
[[ "$(find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' 2>/dev/null | wc -l | tr -d ' ')" == "$REMOTE_BEFORE" ]] || fail "摘要读取失败却留下正式归档"
[[ -z "$(find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age.partial' -print -quit)" ]] || fail "摘要读取失败清理后仍残留 .partial"
cmp -s "$STATUS" "$TMP/status.before" || fail "摘要读取失败更新了成功状态"
ok "异机摘要读取失败清理 .partial 且不更新状态"

# 上传中断在远端产生的半成品也要清理。
REMOTE_BEFORE=$(find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' 2>/dev/null | wc -l | tr -d ' ')
env "${backup_env[@]}" FAKE_SCP_PARTIAL=1 "$ROOT/deploy/backup.sh" >/dev/null 2>&1 || true
REMOTE_AFTER=$(find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' 2>/dev/null | wc -l | tr -d ' ')
[[ "$REMOTE_BEFORE" == "$REMOTE_AFTER" ]] || fail "上传失败留下远端半成品"
cmp -s "$STATUS" "$TMP/status.before" || fail "上传失败改变了成功状态"
ok "异机上传失败清理远端半成品且不更新状态"

# 远端清理也失败时，残留必须只能是 .partial，不会被当成正式归档。
REMOTE_BEFORE=$(find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' 2>/dev/null | wc -l | tr -d ' ')
env "${backup_env[@]}" FAKE_SCP_PARTIAL=1 FAKE_SSH_RM_FAIL=1 "$ROOT/deploy/backup.sh" >/dev/null 2>&1 || true
[[ "$(find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' 2>/dev/null | wc -l | tr -d ' ')" == "$REMOTE_BEFORE" ]] || fail "断线残留被当成正式归档"
[[ -n "$(find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age.partial' -print -quit)" ]] || fail "断线后没有 .partial 残留"
cmp -s "$STATUS" "$TMP/status.before" || fail "断线残留更新了成功状态"
find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age.partial' -delete
ok "远端断线残留只以 .partial 命名"

# 远端校验通过但 rename 失败：不得产生正式归档，也不得更新状态。
REMOTE_BEFORE=$(find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' 2>/dev/null | wc -l | tr -d ' ')
env "${backup_env[@]}" FAKE_SSH_MV_FAIL=1 "$ROOT/deploy/backup.sh" >/dev/null 2>&1 || true
[[ "$(find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' 2>/dev/null | wc -l | tr -d ' ')" == "$REMOTE_BEFORE" ]] || fail "rename 失败却留下正式归档"
[[ -z "$(find "$TMP/remote" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age.partial' -print -quit)" ]] || fail "rename 失败清理后仍残留 .partial"
cmp -s "$STATUS" "$TMP/status.before" || fail "rename 失败更新了成功状态"
ok "异机 rename 失败不产生正式归档且不更新状态"

# local-only 只保留本地归档，不写 offsite 成功状态。
rm -f "$STATUS"
env "${backup_env[@]}" BYEBYEOCS_OFFSITE_SSH="" BYEBYEOCS_OFFSITE_DIR="" \
  "$ROOT/deploy/backup.sh" --local-only >/dev/null
[[ ! -e "$STATUS" ]] || fail "local-only 写了成功状态"
ok "local-only 不伪称 offsite 成功"
# 本地保留上限：连续两次 local-only，保留 1 份。
env "${backup_env[@]}" BYEBYEOCS_LOCAL_RETAIN_COUNT=1 "$ROOT/deploy/backup.sh" --local-only >/dev/null
env "${backup_env[@]}" BYEBYEOCS_LOCAL_RETAIN_COUNT=1 "$ROOT/deploy/backup.sh" --local-only >/dev/null
LOCAL_COUNT=$(find "$TMP/staging" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' | wc -l | tr -d ' ')
[[ "$LOCAL_COUNT" == 1 ]] || fail "local-only 保留数量不受限：$LOCAL_COUNT"
ok "local-only 按保留数量限制本地归档"

# ---------- 并发备份、配置一致性与中断归档 ----------
mkfifo "$TMP/backup-lock.pipe"
(
  exec 9>"$TMP/staging/.backup.lock"
  flock -n 9
  : >"$TMP/backup-lock-ready"
  read -r _ <"$TMP/backup-lock.pipe"
) &
B_LOCK=$!
wait_for_file "$TMP/backup-lock-ready" || fail "未能获取备份锁测试句柄"
expect_fail "并发备份被锁拒绝" env "${backup_env[@]}" "$ROOT/deploy/backup.sh"
printf '\n' >"$TMP/backup-lock.pipe"
wait "$B_LOCK" 2>/dev/null || true
rm -f "$TMP/backup-lock.pipe"

# 题库 DSN 存在但缺少活动配置时，拒绝生成缺配置的“完整”备份。
expect_fail "题库 DSN 缺少配置目录时拒绝备份" env "${backup_env[@]}" BYEBYEOCS_TIKU_CONFIG_DIR="" \
  "$ROOT/deploy/backup.sh"

# staging 位于配置目录内会导致后续备份递归打包旧归档。
expect_fail "拒绝位于配置目录内的备份临时目录" env "${backup_env[@]}" \
  BYEBYEOCS_BACKUP_STAGING="$BACKUPCONFIG/staging" "$ROOT/deploy/backup.sh" --local-only
[[ ! -e "$BACKUPCONFIG/staging" ]] || fail "拒绝配置后仍创建了 staging"
ok "拒绝把 staging 放进被备份的配置目录且不创建目录"

# 组 TERM（systemd/Ctrl-C）必须在退出时清理 staging 明文工作目录和 .partial。
command -v setsid >/dev/null 2>&1 || fail "测试需要 setsid"
FINAL_BEFORE=$(find "$TMP/staging" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' | wc -l | tr -d ' ')
start_new_session "$TMP/backup-term.pid" env "${backup_env[@]}" \
  FAKE_AGE_HANG=1 FAKE_AGE_READY="$TMP/age-term.ready" "$ROOT/deploy/backup.sh" --local-only \
  >"$TMP/backup-term.log" 2>&1
wait_for_file "$TMP/backup-term.pid" || fail "TERM 备份进程未启动"
wait_for_file "$TMP/age-term.ready" || fail "TERM 备份未进入 age 写入阶段"
TERM_PID=$(cat "$TMP/backup-term.pid")
WORK_DIR=$(find "$TMP/staging" -maxdepth 1 -type d -name '.work.*' -print -quit)
[[ -n "$WORK_DIR" ]] || fail "备份未在 staging 内创建明文工作目录"
[[ "$(stat -c %a "$WORK_DIR")" == 700 ]] || fail "明文工作目录权限不是 0700"
kill -TERM -"$TERM_PID" 2>/dev/null || true
for ((i = 0; i < 200; i++)); do
  [[ -z "$(find "$TMP/staging" -maxdepth 1 -type d -name '.work.*' -print -quit 2>/dev/null)" ]] && break
  sleep 0.05
done
[[ -z "$(find "$TMP/staging" -maxdepth 1 -type d -name '.work.*' -print -quit 2>/dev/null)" ]] || fail "TERM 后遗留明文工作目录"
[[ -z "$(find "$TMP/staging" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age.partial' -print -quit 2>/dev/null)" ]] || fail "TERM 后遗留 .partial"
[[ "$(find "$TMP/staging" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' | wc -l | tr -d ' ')" == "$FINAL_BEFORE" ]] || fail "TERM 后产生正式归档"
for ((i = 0; i < 200; i++)); do
  flock -n "$TMP/staging/.backup.lock" true && break
  sleep 0.05
done
ok "信号终止备份清理 staging 明文工作目录和 .partial"

# SIGKILL 中断的归档必须是 .partial，工作目录留给下一次持锁启动清理。
FINAL_BEFORE=$(find "$TMP/staging" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' | wc -l | tr -d ' ')
start_new_session "$TMP/backup-kill.pid" env "${backup_env[@]}" \
  FAKE_AGE_HANG=1 FAKE_AGE_READY="$TMP/age.ready" "$ROOT/deploy/backup.sh" --local-only \
  >"$TMP/backup-kill.log" 2>&1
wait_for_file "$TMP/backup-kill.pid" || fail "备份进程未启动"
wait_for_file "$TMP/age.ready" || fail "备份未进入 age 写入阶段"
KILL_PID=$(cat "$TMP/backup-kill.pid")
WORK_DIR=$(find "$TMP/staging" -maxdepth 1 -type d -name '.work.*' -print -quit)
[[ -n "$WORK_DIR" ]] || fail "SIGKILL 前未创建 staging 明文工作目录"
kill -KILL -"$KILL_PID" 2>/dev/null || true
sleep 0.5
FINAL_AFTER=$(find "$TMP/staging" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' | wc -l | tr -d ' ')
[[ "$FINAL_AFTER" == "$FINAL_BEFORE" ]] || fail "SIGKILL 留下被当成正式归档的文件"
[[ -n "$(find "$TMP/staging" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age.partial' -print -quit)" ]] || fail "中断备份没有 .partial 命名"
[[ -n "$(find "$TMP/staging" -maxdepth 1 -type d -name '.work.*' -print -quit)" ]] || fail "SIGKILL 后没有待清理的明文工作目录"
ok "被 SIGKILL 中断的备份不伪装成正式归档"

env "${backup_env[@]}" BYEBYEOCS_LOCAL_RETAIN_COUNT=1 "$ROOT/deploy/backup.sh" --local-only >/dev/null
[[ -z "$(find "$TMP/staging" -maxdepth 1 -type d -name '.work.*' -print -quit)" ]] || fail "下一次备份未清理旧明文工作目录"
[[ -z "$(find "$TMP/staging" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age.partial' -print -quit)" ]] || fail "下一次备份未清理 .partial"
[[ "$(find "$TMP/staging" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' | wc -l | tr -d ' ')" == 1 ]] || fail "保留计数受 .partial 影响"
ok "下一次备份清理明文工作目录和 .partial 且保留计数只统计完整归档"

expect_fail "拒绝父目录异机路径" env "${backup_env[@]}" BYEBYEOCS_OFFSITE_DIR="/srv/../escape" "$ROOT/deploy/backup.sh"

# 恢复演练假 initdb/pg_ctl，只验证隔离临时集群调用与报告；不执行真实数据库命令。
cat >"$FAKEBIN/initdb" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'initdb\n' >>"${FAKE_RESTORE_LOG:-/dev/null}"
while [[ $# -gt 0 ]]; do case "$1" in -D) mkdir -p "$2";; esac; shift; done
EOF
cat >"$FAKEBIN/pg_ctl" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'pg_ctl\n' >>"${FAKE_RESTORE_LOG:-/dev/null}"
exit 0
EOF
cat >"$FAKEBIN/createdb" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'createdb %s\n' "${@: -1}" >>"${FAKE_RESTORE_LOG:-/dev/null}"
EOF
cat >"$FAKEBIN/pg_restore" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'pg_restore\n' >>"${FAKE_RESTORE_LOG:-/dev/null}"
input=${@: -1}; [[ -f "$input" ]] || exit 9
EOF
cat >"$FAKEBIN/psql" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'psql\n' >>"${FAKE_RESTORE_LOG:-/dev/null}"
exit 0
EOF
chmod 0755 "$FAKEBIN"/initdb "$FAKEBIN"/pg_ctl "$FAKEBIN"/createdb "$FAKEBIN"/pg_restore "$FAKEBIN"/psql
# 用真实 age 加密不可用；这里复用 backup 的 tar 归档，fake age 会原样解密。
DRILL_ARCHIVE=$(ls "$TMP"/staging/*.age 2>/dev/null | head -1 || true)
if [[ -z "$DRILL_ARCHIVE" ]]; then
  # 从远端取回测试归档
  DRILL_ARCHIVE=$(ls "$TMP"/remote/*.age | head -1)
fi
DRILL_REPORT="$TMP/drill-report.json"
FAKE_RESTORE_LOG="$TMP/restore.log" "$ROOT/deploy/restore-drill.sh" \
  --archive "$DRILL_ARCHIVE" --identity "$TMP/identity" --report "$DRILL_REPORT" >/dev/null
[[ -f "$DRILL_REPORT" ]] || fail "恢复演练报告缺失"
python3 - "$DRILL_REPORT" <<'PY'
import json,sys
r=json.load(open(sys.argv[1]))
assert r["status"]=="passed" and r["production_untouched"] is True
PY
ok "隔离恢复演练使用临时集群并输出独立报告"

# 恢复演练拒绝符号链接条目，且不触碰临时 PostgreSQL。
BADROOT="$TMP/badroot"
mkdir -p "$BADROOT"
ln -s /etc/passwd "$BADROOT/evil"
tar -cf "$TMP/bad-archive.tar" -C "$BADROOT" evil
FAKE_RESTORE_LOG="$TMP/restore-bad.log" expect_fail "恢复演练拒绝符号链接归档"   "$ROOT/deploy/restore-drill.sh" --archive "$TMP/bad-archive.tar" --identity "$TMP/identity"
[[ ! -s "$TMP/restore-bad.log" ]] || fail "不安全归档仍启动了恢复命令"

# manifest 声明题库数据库却缺少对应 dump 时，不能报告恢复通过。
BADMANIFEST="$TMP/bad-manifest"
mkdir -p "$BADMANIFEST/database" "$BADMANIFEST/config/main"
printf 'main\n' >"$BADMANIFEST/database/main.dump"
printf 'config\n' >"$BADMANIFEST/config/main/config.yaml"
cat >"$BADMANIFEST/manifest.txt" <<'EOF'
format=1
dump_started_at=2026-01-01T00:00:00Z
main_database=true
tiku_database=true
config_main=true
config_tiku=false
EOF
(
  cd "$BADMANIFEST"
  find . -type f ! -name manifest.txt -print0 | sort -z | xargs -0 sha256sum
) >>"$BADMANIFEST/manifest.txt"
tar -cf "$TMP/bad-manifest.tar" -C "$BADMANIFEST" .
: >"$TMP/restore-manifest.log"
FAKE_RESTORE_LOG="$TMP/restore-manifest.log" expect_fail "恢复演练拒绝 manifest 缺失数据库声明" \
  "$ROOT/deploy/restore-drill.sh" --archive "$TMP/bad-manifest.tar" --identity "$TMP/identity"
[[ ! -s "$TMP/restore-manifest.log" ]] || fail "manifest 与内容不一致仍启动了恢复命令"
ok "恢复演练拒绝 manifest 与归档内容不一致"

# ---------- 发布目录：可读权限与失败不破坏旧产物（假项目、假 go/git） ----------
BUNDLE_TMP="$TMP/bundle"
FAKE_PROJECT="$BUNDLE_TMP/project"
BUNDLE_BIN="$BUNDLE_TMP/bin"
mkdir -p "$FAKE_PROJECT/scripts" \
  "$FAKE_PROJECT/server/internal/platform/postgres/migrations" \
  "$FAKE_PROJECT/services/tiku/internal/platform/store/migrations" \
  "$FAKE_PROJECT/apps/admin/dist" "$FAKE_PROJECT/apps/client_flutter/build/web" \
  "$FAKE_PROJECT/build/bin" "$FAKE_PROJECT/docs" "$FAKE_PROJECT/api" \
  "$BUNDLE_BIN" "$BUNDLE_TMP/goroot"
cp "$ROOT/scripts/build-bundle.sh" "$ROOT/scripts/collect-notices.py" "$FAKE_PROJECT/scripts/"
cp -a "$ROOT/deploy/." "$FAKE_PROJECT/deploy/"
cp -a "$ROOT/configs/." "$FAKE_PROJECT/configs/"
printf 'design\n' >"$FAKE_PROJECT/DESIGN.md"
printf 'doc\n' >"$FAKE_PROJECT/docs/index.md"
printf 'api\n' >"$FAKE_PROJECT/api/openapi.yaml"
printf '{"packages":{}}\n' >"$FAKE_PROJECT/apps/admin/package-lock.json"
printf '<html>admin</html>\n' >"$FAKE_PROJECT/apps/admin/dist/index.html"
printf '<html>client</html>\n' >"$FAKE_PROJECT/apps/client_flutter/build/web/index.html"
printf '#!/bin/sh\nexit 0\n' >"$FAKE_PROJECT/build/bin/byebyeocs"
printf '#!/bin/sh\nexit 0\n' >"$FAKE_PROJECT/build/bin/ocs-tiku-fork"
chmod 0755 "$FAKE_PROJECT/build/bin/byebyeocs" "$FAKE_PROJECT/build/bin/ocs-tiku-fork"
printf -- '-- 0001\n' >"$FAKE_PROJECT/server/internal/platform/postgres/migrations/0001_a.sql"
printf -- '-- 0007\n' >"$FAKE_PROJECT/server/internal/platform/postgres/migrations/0007_b.sql"
printf -- '-- 0002\n' >"$FAKE_PROJECT/services/tiku/internal/platform/store/migrations/0002_b.sql"
printf 'license\n' >"$FAKE_PROJECT/services/tiku/LICENSE"
printf 'readme\n' >"$FAKE_PROJECT/services/tiku/README.md"
printf 'upstream\n' >"$FAKE_PROJECT/services/tiku/UPSTREAM.md"
cat >"$BUNDLE_BIN/go" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == env ]]; then
  case "${2:-}" in
    GOROOT) printf '%s\n' "$FAKE_GOROOT";;
    GOVERSION) printf 'go1.99\n';;
  esac
fi
exit 0
EOF
cat >"$BUNDLE_BIN/git" <<'EOF'
#!/usr/bin/env bash
printf 'deadbeef\n'
EOF
chmod 0755 "$BUNDLE_BIN/go" "$BUNDLE_BIN/git"
build_fake_bundle() {
  FAKE_GOROOT="$BUNDLE_TMP/goroot" PATH="$BUNDLE_BIN:$PATH" \
    BYEBYEOCS_BUNDLE_BIN_DIR="$FAKE_PROJECT/build/bin" \
    bash "$FAKE_PROJECT/scripts/build-bundle.sh" 9.9.9 amd64
}
build_fake_bundle >/dev/null || fail "假项目发布构建失败"
FAKE_DIST="$FAKE_PROJECT/dist/byebyeocs-9.9.9-linux-amd64"
[[ -f "$FAKE_DIST/release.json" && -f "$FAKE_DIST/checksums.txt" ]] || fail "假项目发布目录缺少清单"
(cd "$FAKE_DIST" && sha256sum -c --quiet checksums.txt) || fail "假项目发布目录自校验失败"
for metadata in release.json checksums.txt; do
  mode=$(stat -c %a "$FAKE_DIST/$metadata")
  if (( (8#$mode & 4) == 0 )); then fail "$metadata 不可被服务用户读取：$mode"; fi
done
ok "发布清单与校验文件权限允许非 root 服务用户读取"

mv "$FAKE_PROJECT/apps/admin/dist/index.html" "$BUNDLE_TMP/admin-index.html"
if build_fake_bundle >/dev/null 2>&1; then fail "缺少静态应用时发布构建应失败"; fi
[[ -f "$FAKE_DIST/release.json" ]] || fail "发布构建失败删除了上一次可用产物"
(cd "$FAKE_DIST" && sha256sum -c --quiet checksums.txt) || fail "发布构建失败损坏了上一次产物"
[[ -z "$(find "$FAKE_PROJECT/dist" -maxdepth 1 -name '.bundle-*' -print -quit)" ]] || fail "发布构建失败留下临时目录"
ok "发布构建失败保留上一次可用产物并清理临时目录"

printf '全部部署脚本测试通过（%d 项）。\n' "$PASS"
