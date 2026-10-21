#!/usr/bin/env bash
# 用当前构建的正式 CLI 验证公开配置样例字段；不连接数据库、不启动服务。
set -Eeuo pipefail
umask 077

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SERVER_BIN=${BYEBYEOCS_SERVER_BIN:-$ROOT/build/bin/byebyeocs}
TIKU_BIN=${BYEBYEOCS_TIKU_BIN:-$ROOT/build/bin/ocs-tiku-fork}
[[ -x "$SERVER_BIN" ]] || { printf '错误：缺少 %s，先执行 make server\n' "$SERVER_BIN" >&2; exit 1; }
[[ -x "$TIKU_BIN" ]] || { printf '错误：缺少 %s，先执行 make tiku\n' "$TIKU_BIN" >&2; exit 1; }

TMP=$(mktemp -d "${TMPDIR:-/tmp}/byebyeocs-config-test.XXXXXXXX")
trap 'rm -rf -- "$TMP"' EXIT
mkdir -p "$TMP/secrets"
cp "$ROOT/configs/server.example.yaml" "$TMP/config.yaml"
head -c 32 /dev/urandom | base64 >"$TMP/secrets/application-v1.key"
head -c 32 /dev/urandom | base64 >"$TMP/secrets/identity.key"
printf '%s\n' 'postgres://user:pass@127.0.0.1:5432/byebyeocs?sslmode=disable' >"$TMP/secrets/database.dsn"
chmod 0600 "$TMP"/secrets/*
"$SERVER_BIN" config check --config "$TMP/config.yaml" >/dev/null

mkdir -p "$TMP/tiku/secrets"
cp "$ROOT/configs/ocs-tiku.example.json" "$TMP/tiku/ocs-tiku-fork.json"
printf '%s\n' 'postgres://user:pass@127.0.0.1:5432/ocs_tiku?sslmode=disable' >"$TMP/tiku/secrets/database.dsn"
printf '%s\n' '0123456789abcdef0123456789abcdef' >"$TMP/tiku/secrets/service-token"
chmod 0600 "$TMP/tiku/secrets"/*
(cd "$TMP/tiku" && "$TIKU_BIN" config check -config ocs-tiku-fork.json >/dev/null)

printf '公开主服务与题库配置样例通过正式 CLI 校验。\n'
