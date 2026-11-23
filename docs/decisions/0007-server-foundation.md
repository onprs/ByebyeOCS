# 决策 0007：Go 主服务基础底座与回环双监听隔离

- **状态**：已采纳并落地

## 背景

正式后端需要一方面与保留在本地的早期协议研究目录隔离编译依赖，另一方面从网络监听层彻底分开普通用户流量与后台管理流量，并避免在配置中散落明文连接串或启动时静默修改生产数据库。

## 决策

1. 建立独立的 `server/` Go 模块（`CGO_ENABLED=0` 静态构建），将研究源码完全排除在正式构建与测试图之外。
2. 单进程内置两个强制监听回环 IP 且端口互不相同的 HTTP 服务（默认用户监听 `127.0.0.1:8090`、管理监听 `127.0.0.1:8091`），公网访问一律经过反向代理终结 TLS，默认仅信任回环代理转发的客户端地址。
3. 数据库 DSN 与各类密钥文件禁止直接写入 YAML 正文，强制从受权限保护（非 Windows 校验 `0600`）的独立文件或声明的环境变量读取；配置解析拒绝未知字段。
4. CLI 显式拆分 `config init`、`config check`、`migrate up`、`migrate status`、`doctor`、`admin create`、`keys` 与 `serve`，`serve` 启动前只读校验迁移完整性，绝不自动建表或生成默认管理员。

## 当前落点与影响

- 实现位于 `server/cmd/byebyeocs`、`server/internal/command`、`server/internal/platform/config` 与 `server/internal/app/server.go`，命令行详情见 [后端 CLI 与测试基础手册](../development/backend-foundation.md)。
