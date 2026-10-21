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

"$V1/deploy/uninstall.sh" --prefix "$PREFIX" --config-dir "$CONFIG" --data-dir "$DATA" \
  --tiku-config-dir "$TIKU_CONFIG" --no-systemd >"$TMP/uninstall.log"
[[ ! -e "$PREFIX/releases" && ! -e "$PREFIX/current" ]] || fail "uninstall 移除程序"
[[ "$(cat "$CONFIG/config.yaml")" == "KEEP-CONFIG" && "$(cat "$DATA/sentinel")" == "KEEP-DATA" ]] || fail "uninstall 保留数据"
ok "卸载保留配置/密钥/数据"

expect_fail "拒绝根目录前缀" "$V1/deploy/install.sh" --prefix / --no-systemd --no-service-user
expect_fail "拒绝根目录配置" "$V1/deploy/install.sh" --prefix "$TMP/p2" --config-dir / --no-systemd --no-service-user

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
if [[ "$decrypt" == 1 ]]; then cp -- "$input" "$out"; else cat >"$out"; fi
EOF
cat >"$FAKEBIN/scp" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
src=${@: -2:1}; dst=${@: -1}
mkdir -p "$FAKE_REMOTE_DIR"
cp -- "$src" "$FAKE_REMOTE_DIR/$(basename "$src")"
EOF
cat >"$FAKEBIN/ssh" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
cmd=${@: -1}
if [[ "$cmd" == *machine-id* ]]; then
  printf '%s\n' "${FAKE_REMOTE_MACHINE_ID:-remote-machine-id}"
  exit 0
fi
path=$(printf '%s' "$cmd" | sed -n "s/.*sha256sum -- '\([^']*\)'.*/\1/p")
[[ -n "$path" ]] || exit 9
name=$(basename "$path")
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
import json,glob,sys
status=json.load(open(sys.argv[1]))
assert status["status"]=="ok" and status["offsite"] is True
assert status["last_recoverable_at"] and status["verified_at"]
assert status["restore_drill_verified"] is False
files=glob.glob(sys.argv[2]+"/*.age")
assert files, "没有异机归档"
PY
ok "备份成功只在异机校验后写 ok/offsite 状态"

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

# 失败不能覆盖旧的 ok 状态，也不能在本地留下本次完整归档。
cp "$STATUS" "$TMP/status.before"
STAGING_BEFORE=$(find "$TMP/staging" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' 2>/dev/null | wc -l | tr -d ' ')
env "${backup_env[@]}" FAKE_SSH_BAD=1 "$ROOT/deploy/backup.sh" >/dev/null 2>"$TMP/backup-fail.log" || true
cmp -s "$STATUS" "$TMP/status.before" || fail "远端校验失败却更新了成功状态"
STAGING_AFTER=$(find "$TMP/staging" -maxdepth 1 -name 'byebyeocs-backup-*.tar.age' 2>/dev/null | wc -l | tr -d ' ')
[[ "$STAGING_BEFORE" == "$STAGING_AFTER" ]] || fail "远端失败留下本次完整归档"
ok "异机校验失败不更新状态且清理本次归档"

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

printf '全部部署脚本测试通过（%d 项）。\n' "$PASS"
