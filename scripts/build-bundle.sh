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

mkdir -p -- "$DIST_ROOT"
BUILD_DIR=$(mktemp -d "$DIST_ROOT/.bundle-$VERSION-$GOARCH.XXXXXXXX")
PREVIOUS=""
cleanup() {
  if [[ -n "${BUILD_DIR:-}" ]]; then
    rm -rf -- "$BUILD_DIR"
  fi
  if [[ -n "${PREVIOUS:-}" ]]; then
    if [[ ! -e "$DIST" ]]; then
      mv -T -- "$PREVIOUS" "$DIST" || true
    else
      rm -rf -- "$PREVIOUS"
    fi
  fi
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p -- "$BUILD_DIR/bin" "$BUILD_DIR/apps/admin" "$BUILD_DIR/apps/client-web" \
  "$BUILD_DIR/configs" "$BUILD_DIR/deploy" "$BUILD_DIR/docs" "$BUILD_DIR/THIRD_PARTY_NOTICES"

install -m 0755 "$SERVER_BIN" "$BUILD_DIR/bin/byebyeocs"
install -m 0755 "$TIKU_BIN" "$BUILD_DIR/bin/ocs-tiku-fork"
cp -a -- "$ADMIN/." "$BUILD_DIR/apps/admin/"
cp -a -- "$CLIENT/." "$BUILD_DIR/apps/client-web/"
cp -a -- "$ROOT/configs/." "$BUILD_DIR/configs/"
cp -a -- "$ROOT/deploy/." "$BUILD_DIR/deploy/"
find "$BUILD_DIR/deploy" -type d -name '__pycache__' -prune -exec rm -rf -- {} +
find "$BUILD_DIR/deploy" -type f -name '*.pyc' -delete
cp -a -- "$ROOT/docs/." "$BUILD_DIR/docs/"
mkdir -p "$BUILD_DIR/api"
cp -a -- "$ROOT/api/." "$BUILD_DIR/api/"
cp -a -- "$ROOT/DESIGN.md" "$BUILD_DIR/DESIGN.md"
python3 "$ROOT/scripts/collect-notices.py" "$BUILD_DIR/THIRD_PARTY_NOTICES" "$GOARCH"
mkdir -p "$BUILD_DIR/THIRD_PARTY_NOTICES/ocs-tiku-fork"
for notice in LICENSE README.md UPSTREAM.md; do
  cp -a -- "$ROOT/services/tiku/$notice" "$BUILD_DIR/THIRD_PARTY_NOTICES/ocs-tiku-fork/$notice"
done
LICENSE_ENTRY=null
PROJECT_LICENSE=not_declared
if [[ -n "$LICENSE_FILE" ]]; then
  cp -a -- "$LICENSE_FILE" "$BUILD_DIR/LICENSE"
  LICENSE_ENTRY='"LICENSE"'
  PROJECT_LICENSE=see_license_file
fi
# 保留仓库根目录已有的第三方 NOTICE/COPYING；静态应用目录中的 NOTICES 随应用一起复制。
for notice in "$ROOT"/NOTICE* "$ROOT"/COPYING*; do
  [[ -f "$notice" ]] || continue
  cp -a -- "$notice" "$BUILD_DIR/THIRD_PARTY_NOTICES/"
done
if [[ -f "$ROOT/README.md" ]]; then
  cp -a -- "$ROOT/README.md" "$BUILD_DIR/README.md"
fi

if find "$BUILD_DIR" -type f \( -iname '*.apk' -o -iname '*debug*' \) -print -quit | grep -q .; then
  printf '错误：发布包不应包含 APK 或 debug 构建产物\n' >&2
  exit 1
fi
if find "$BUILD_DIR" -type l -print -quit | grep -q .; then
  printf '错误：发布包不应包含符号链接\n' >&2
  exit 1
fi

COMMIT=$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || printf 'unknown')
BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
MAIN_SCHEMA=$(find "$ROOT/server/internal/platform/postgres/migrations" -maxdepth 1 -name '*.sql' -printf '%f\n' | sed -n 's/^0*\([0-9][0-9]*\)_.*/\1/p' | sort -n | tail -1)
TIKU_SCHEMA=$(find "$ROOT/services/tiku/internal/platform/store/migrations" -maxdepth 1 -name '*.sql' -printf '%f\n' | sed -n 's/^0*\([0-9][0-9]*\)_.*/\1/p' | sort -n | tail -1)
[[ -n "$MAIN_SCHEMA" && -n "$TIKU_SCHEMA" ]] || { printf '错误：未找到数据库迁移版本，拒绝生成无效 release.json\n' >&2; exit 1; }
TIKU_COMMIT=$(git -C "$ROOT/services/tiku" rev-parse HEAD)
cat >"$BUILD_DIR/release.json" <<EOF
{"format":1,"version":"$VERSION","goos":"linux","goarch":"$GOARCH","commit":"$COMMIT","build_time":"$BUILD_TIME","main_schema":$MAIN_SCHEMA,"tiku_schema":$TIKU_SCHEMA,"tiku_repository":"https://github.com/onprs/ByebyeOCS-Tiku","tiku_commit":"$TIKU_COMMIT","project_license":"$PROJECT_LICENSE","license_file":$LICENSE_ENTRY,"third_party_notices":"THIRD_PARTY_NOTICES"}
EOF

# 归档只包含公开产物，避免私有umask使专用服务用户无法读取清单或执行程序。
chmod -R a+rX -- "$BUILD_DIR"
(
  cd "$BUILD_DIR"
  find . -type f ! -name checksums.txt -print0 | sort -z | xargs -0 sha256sum
) >"$BUILD_DIR/checksums.txt"
chmod 0644 "$BUILD_DIR/checksums.txt"
(cd "$BUILD_DIR" && sha256sum -c --quiet checksums.txt) || { printf '错误：发布包自校验失败\n' >&2; exit 1; }

# 构建并校验全部通过后才替换旧发布目录；失败时保留上一次可用产物。
if [[ -e "$DIST" ]]; then
  PREVIOUS="$DIST_ROOT/.previous-$VERSION-$GOARCH.$$"
  rm -rf -- "$PREVIOUS"
  mv -T -- "$DIST" "$PREVIOUS"
fi
if ! mv -T -- "$BUILD_DIR" "$DIST"; then
  if [[ -n "$PREVIOUS" && ! -e "$DIST" ]]; then
    mv -T -- "$PREVIOUS" "$DIST" || true
  fi
  printf '错误：无法启用发布目录 %s\n' "$DIST" >&2
  exit 1
fi
BUILD_DIR=""
if [[ -n "$PREVIOUS" ]]; then
  rm -rf -- "$PREVIOUS"
  PREVIOUS=""
fi

printf '发布目录：%s\n' "$DIST"
printf '文件数：%s\n' "$(find "$DIST" -type f | wc -l | tr -d ' ')"
printf '校验文件：%s\n' "$DIST/checksums.txt"
