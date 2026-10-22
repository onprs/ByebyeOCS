#!/usr/bin/env bash
# 生成可审计的 Linux 发布目录；不引用任何不存在的 Release 下载地址。
set -Eeuo pipefail
umask 077

VERSION=${1:-}
GOARCH=${2:-}
[[ -n "$VERSION" && "$VERSION" != "dev" ]] || { printf '错误：必须提供明确的发布版本，例如 VERSION=1.2.3\n' >&2; exit 1; }
[[ "$VERSION" =~ ^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$ ]] || { printf '错误：版本号格式无效\n' >&2; exit 1; }
case "$GOARCH" in amd64|arm64) ;; *) printf '错误：GOARCH 只支持 amd64 或 arm64\n' >&2; exit 1 ;; esac

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
DIST_ROOT="$ROOT/dist"
DIST="$DIST_ROOT/byebyeocs-$VERSION-linux-$GOARCH"
ADMIN="$ROOT/apps/admin/dist"
CLIENT="$ROOT/apps/client_flutter/build/web"
BINARY_DIR=${BYEBYEOCS_BUNDLE_BIN_DIR:-$ROOT/build/bin}
SERVER_BIN="$BINARY_DIR/byebyeocs"
TIKU_BIN="$BINARY_DIR/ocs-tiku-fork"

[[ -x "$SERVER_BIN" ]] || { printf '错误：缺少 %s，先执行 make server\n' "$SERVER_BIN" >&2; exit 1; }
[[ -x "$TIKU_BIN" ]] || { printf '错误：缺少 %s，先执行 make tiku\n' "$TIKU_BIN" >&2; exit 1; }
[[ -f "$ADMIN/index.html" ]] || { printf '错误：缺少管理员静态应用 %s\n' "$ADMIN" >&2; exit 1; }
[[ -f "$CLIENT/index.html" ]] || { printf '错误：缺少 Flutter Web 静态应用 %s\n' "$CLIENT" >&2; exit 1; }

# 项目许可证与已有 NOTICE 原样收录；第三方许可分别归档。
LICENSE_FILE=${LICENSE_FILE:-}
if [[ -z "$LICENSE_FILE" ]]; then
  for candidate in "$ROOT/LICENSE" "$ROOT/LICENSE.md" "$ROOT/LICENSE.txt"; do
    if [[ -f "$candidate" ]]; then LICENSE_FILE=$candidate; break; fi
  done
fi

rm -rf -- "$DIST"
mkdir -p -- "$DIST/bin" "$DIST/apps/admin" "$DIST/apps/client-web" \
  "$DIST/configs" "$DIST/deploy" "$DIST/docs" "$DIST/THIRD_PARTY_NOTICES"

install -m 0755 "$SERVER_BIN" "$DIST/bin/byebyeocs"
install -m 0755 "$TIKU_BIN" "$DIST/bin/ocs-tiku-fork"
cp -a -- "$ADMIN/." "$DIST/apps/admin/"
cp -a -- "$CLIENT/." "$DIST/apps/client-web/"
cp -a -- "$ROOT/configs/." "$DIST/configs/"
cp -a -- "$ROOT/deploy/." "$DIST/deploy/"
find "$DIST/deploy" -type d -name '__pycache__' -prune -exec rm -rf -- {} +
find "$DIST/deploy" -type f -name '*.pyc' -delete
cp -a -- "$ROOT/docs/." "$DIST/docs/"
mkdir -p "$DIST/api"
cp -a -- "$ROOT/api/." "$DIST/api/"
cp -a -- "$ROOT/DESIGN.md" "$DIST/DESIGN.md"
python3 "$ROOT/scripts/collect-notices.py" "$DIST/THIRD_PARTY_NOTICES" "$GOARCH"
mkdir -p "$DIST/THIRD_PARTY_NOTICES/ocs-tiku-fork"
for notice in LICENSE README.md UPSTREAM.md; do
  cp -a -- "$ROOT/services/tiku/$notice" "$DIST/THIRD_PARTY_NOTICES/ocs-tiku-fork/$notice"
done
LICENSE_ENTRY=null
PROJECT_LICENSE=not_declared
if [[ -n "$LICENSE_FILE" ]]; then
  cp -a -- "$LICENSE_FILE" "$DIST/LICENSE"
  LICENSE_ENTRY='"LICENSE"'
  PROJECT_LICENSE=see_license_file
fi
# 保留仓库根目录已有的第三方 NOTICE/COPYING；静态应用目录中的 NOTICES 随应用一起复制。
for notice in "$ROOT"/NOTICE* "$ROOT"/COPYING*; do
  [[ -f "$notice" ]] || continue
  cp -a -- "$notice" "$DIST/THIRD_PARTY_NOTICES/"
done
if [[ -f "$ROOT/README.md" ]]; then
  cp -a -- "$ROOT/README.md" "$DIST/README.md"
fi

if find "$DIST" -type f \( -iname '*.apk' -o -iname '*debug*' \) -print -quit | grep -q .; then
  printf '错误：发布包不应包含 APK 或 debug 构建产物\n' >&2
  exit 1
fi
if find "$DIST" -type l -print -quit | grep -q .; then
  printf '错误：发布包不应包含符号链接\n' >&2
  exit 1
fi

# 归档只包含公开产物，避免私有umask使专用服务用户无法执行安装后的程序。
chmod -R a+rX -- "$DIST"

COMMIT=$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || printf 'unknown')
BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
MAIN_SCHEMA=$(find "$ROOT/server/internal/platform/postgres/migrations" -maxdepth 1 -name '*.sql' -printf '%f\n' | sed -n 's/^0*\([0-9][0-9]*\)_.*/\1/p' | sort -n | tail -1)
TIKU_SCHEMA=$(find "$ROOT/services/tiku/internal/platform/store/migrations" -maxdepth 1 -name '*.sql' -printf '%f\n' | sed -n 's/^0*\([0-9][0-9]*\)_.*/\1/p' | sort -n | tail -1)
TIKU_COMMIT=$(git -C "$ROOT/services/tiku" rev-parse HEAD)
cat >"$DIST/release.json" <<EOF
{"format":1,"version":"$VERSION","goos":"linux","goarch":"$GOARCH","commit":"$COMMIT","build_time":"$BUILD_TIME","main_schema":$MAIN_SCHEMA,"tiku_schema":$TIKU_SCHEMA,"tiku_repository":"https://github.com/onprs/ByebyeOCS-Tiku","tiku_commit":"$TIKU_COMMIT","project_license":"$PROJECT_LICENSE","license_file":$LICENSE_ENTRY,"third_party_notices":"THIRD_PARTY_NOTICES"}
EOF

(
  cd "$DIST"
  find . -type f ! -name checksums.txt -print0 | sort -z | xargs -0 sha256sum
) >"$DIST/checksums.txt"
(cd "$DIST" && sha256sum -c --quiet checksums.txt) || { printf '错误：发布包自校验失败\n' >&2; exit 1; }

printf '发布目录：%s\n' "$DIST"
printf '文件数：%s\n' "$(find "$DIST" -type f | wc -l | tr -d ' ')"
printf '校验文件：%s\n' "$DIST/checksums.txt"
