SHELL := /bin/bash

VERSION ?= dev
GOARCH ?= $(shell go env GOARCH)
FLUTTER ?= flutter
BUILD_DIR ?= build
BIN_DIR := $(BUILD_DIR)/bin
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/onprs/ByebyeOCS/server/internal/command.Version=$(VERSION) \
	-X github.com/onprs/ByebyeOCS/server/internal/command.Commit=$(COMMIT) \
	-X github.com/onprs/ByebyeOCS/server/internal/command.BuildTime=$(BUILD_TIME)

.PHONY: help server tiku admin flutter-web build \
	check checks check-go check-admin check-flutter \
	test test-go test-admin test-flutter test-integration test-deploy \
	bundle clean

help:
	@printf '%s\n' \
		'常用目标：' \
		'  make server              构建主 Go 二进制到 build/bin/byebyeocs' \
		'  make tiku                构建独立题库 Go 二进制到 build/bin/ocs-tiku-fork' \
		'  make admin               构建管理员静态应用' \
		'  make flutter-web         构建 Flutter Web release（需要 Flutter SDK）' \
		'  make build               构建全部四个产物' \
		'  make check               完整静态检查（Go + 管理端 + Flutter）' \
		'  make tests               完整单元/契约测试（Go + 管理端 + Flutter）' \
		'  make test-integration    全部 Go 包 + 隔离 PostgreSQL 集成测试' \
		'  make test-deploy         部署/备份脚本隔离测试（不实际 SSH）' \
		'  make bundle VERSION=x.y.z GOARCH=amd64  生成带 checksums.txt 的发布目录' \
		'  make clean               清理 build/ 和 dist/' \
		'' \
		'项目许可见 LICENSE；bundle 同时收录项目许可证与第三方 NOTICE。'

server:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go -C server build -trimpath -ldflags '$(LDFLAGS)' -o ../$(BIN_DIR)/byebyeocs ./cmd/byebyeocs

tiku:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go -C services/tiku build -trimpath -o ../../$(BIN_DIR)/ocs-tiku-fork ./cmd/ocs-tiku-fork

admin:
	npm --prefix apps/admin ci
	npm --prefix apps/admin run build

flutter-web:
	@command -v $(FLUTTER) >/dev/null 2>&1 || { printf '错误：未找到 Flutter SDK；请设置 FLUTTER=/path/to/flutter 后重试。\n' >&2; exit 1; }
	cd apps/client_flutter && $(FLUTTER) pub get && $(FLUTTER) build web --release --no-web-resources-cdn

build: server tiku admin flutter-web

check-go:
	@set -e; files=$$(gofmt -l server/cmd server/internal services/tiku); if [[ -n "$$files" ]]; then printf '需要 gofmt：\n%s\n' "$$files" >&2; exit 1; fi
	go -C server vet ./...
	go -C services/tiku vet ./...

check-admin:
	npm --prefix apps/admin ci
	npm --prefix apps/admin run check

check-flutter:
	@command -v $(FLUTTER) >/dev/null 2>&1 || { printf '错误：未找到 Flutter SDK；请设置 FLUTTER=/path/to/flutter 后重试。\n' >&2; exit 1; }
	cd apps/client_flutter && $(FLUTTER) pub get && $(FLUTTER) analyze && $(FLUTTER) test

check: check-go check-admin check-flutter
checks: check

test-go:
	go -C server test -race ./...
	go -C services/tiku test -race ./...

test-admin:
	npm --prefix apps/admin ci
	npm --prefix apps/admin test

test-flutter:
	@command -v $(FLUTTER) >/dev/null 2>&1 || { printf '错误：未找到 Flutter SDK；请设置 FLUTTER=/path/to/flutter 后重试。\n' >&2; exit 1; }
	cd apps/client_flutter && $(FLUTTER) pub get && $(FLUTTER) test

test: test-go test-admin test-flutter
tests: test

test-integration:
	@test -n "$$BYEBYEOCS_TEST_DATABASE_DSN_FILE" || { printf '错误：请设置 BYEBYEOCS_TEST_DATABASE_DSN_FILE 为隔离 PostgreSQL 连接文件。\n' >&2; exit 1; }
	BYEBYEOCS_TEST_DATABASE_DSN_FILE="$$BYEBYEOCS_TEST_DATABASE_DSN_FILE" go -C server test -race -tags=integration ./...
	BYEBYEOCS_TEST_DATABASE_DSN_FILE="$$BYEBYEOCS_TEST_DATABASE_DSN_FILE" go -C services/tiku test -race ./...

test-configs: server tiku
	bash scripts/test-configs.sh

test-deploy:
	bash scripts/test-deploy.sh

bundle: server tiku admin flutter-web
	@test "$(VERSION)" != "dev" || { printf '错误：bundle 需要明确的 VERSION，例如 VERSION=1.2.3。\n' >&2; exit 1; }
	BYEBYEOCS_BUNDLE_BIN_DIR="$(abspath $(BIN_DIR))" bash scripts/build-bundle.sh "$(VERSION)" "$(GOARCH)"

clean:
	rm -rf -- "$(BUILD_DIR)" dist
