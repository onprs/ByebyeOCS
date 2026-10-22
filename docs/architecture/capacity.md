# 容量规划与低内存部署

本文面向公开Linux原生部署环境。正式任务执行使用HTTP请求，不运行浏览器、播放器或媒体解码器。主服务、独立题库、PostgreSQL及HTTPS代理需要分别控制资源；Web静态资源和App在构建环境生成。

2C1G可作为低并发试用规格；2C4G/30GB保留为有更多余量的规划基线。CPU、内存、磁盘和并发需要结合实际发行版、活跃任务及外部请求现场验证，尚不承诺真实用户数量。

## 1. 已完成的1GB受限组件验证

维护环境将以下组件置于同一个Linux cgroup：

- PostgreSQL16.15，同一集群中的主库与题库使用独立数据库、角色。
- 正式Go主服务和正式题库二进制。
- 原生HTTP执行器的本地TLS协议夹具，完成60秒普通请求执行。
- 两次管理员密码校验、40次已认证管理API读取及40次静态入口读取。
- 一次真实pg_dump双数据库备份及age加密，保留为本地归档。

控制组内存上限为1000000000字节（内核页对齐后999997440字节），交换空间上限0，CPU配额200%。最终源码复验完成上述场景的控制组内存峰值为342994944字节，约327.1 MiB；首轮为328.4 MiB。两轮均未触发内存上限或OOM。该场景没有真实平台、模型或资金流量。

这些数据包含组件进程及控制组计入的文件缓存，尚未包含宿主系统、HTTPS代理、完整外部连接、长期业务数据、大图/大导出及长期压力。资源夹具与实际云主机可能具有不同的共享页缓存和CPU调度条件。因此，该结果支持低并发试用，实际部署仍须保留系统余量并检查峰值。

## 2. 低内存起点

下表给出低内存配置起点。Go软目标、连接池、并发和PostgreSQL参数来自本次场景；分服务systemd限额是部署建议，需结合现场峰值核对。安装完整发布目录、准备数据库和双端来源后，在现有配置中调整对应字段。

| 项目 | 低内存起点 |
| --- | --- |
| 主服务Go内存软目标 | `GOMEMLIMIT=192MiB` |
| 主服务控制组 | `MemoryHigh=256M`、`MemoryMax=384M` |
| 题库Go内存软目标 | `GOMEMLIMIT=96MiB` |
| 题库控制组 | `MemoryHigh=128M`、`MemoryMax=192M` |
| 主服务数据库池 | `database.max_connections: 4` |
| 题库数据库池 | `database_max_connections: 2` |
| 学习执行并发 | `learning.global_concurrency: 1`、`learning.account_concurrency: 1` |
| 主平台与题库查询并发 | 主配置`questions.concurrency: 1`，题库`limits.concurrency: 1` |
| 题库请求与图片 | 请求8 MiB，最多2张图，每张2 MiB |
| PostgreSQL | `shared_buffers=64MB`、`work_mem=1MB`、`maintenance_work_mem=16MB`、`max_connections=20` |

主服务与题库运行配置相互独立，两侧的并发都需要调整。PostgreSQL的`work_mem`会被多个查询节点和连接分别使用；上述值需要结合报表、索引维护和备份校准。正常的持久化、账务事务和密码哈希强度保持启用。

项目提供两个可选systemd内存配置。在发布目录中、首次启动前安装：

```sh
sudo install -d /etc/systemd/system/byebyeocs.service.d /etc/systemd/system/ocs-tiku.service.d
sudo install -m 0644 deploy/systemd/byebyeocs-low-memory.conf /etc/systemd/system/byebyeocs.service.d/memory.conf
sudo install -m 0644 deploy/systemd/ocs-tiku-low-memory.conf /etc/systemd/system/ocs-tiku.service.d/memory.conf
sudo systemctl daemon-reload
```

题库部署在别处时只安装主服务配置。修改数据库、并发和内存设置后先运行配置检查，再按[部署指南](../../deploy/README.md)启动。已有实例在维护窗口应用设置。生产内存上限要结合现场峰值调整；交换空间只能用于短时缓冲。

## 3. 默认部署与4GB规划

默认主服务Go内存软目标512MiB、控制组硬上限768M；题库控制组硬上限384M。主服务数据库池默认12，题库池默认4，学习并发4、账号并发1、题库并发2。内存软目标和控制组上限不代表进程的固定常驻占用。

4GB规划为数据库、系统缓存、较大的业务数据及操作峰值保留更多空间。原有工程预算为：主服务384–640 MiB、题库128–256 MiB、PostgreSQL768–1024 MiB、系统/代理512–768 MiB、备份增量128–256 MiB，并尽量保留约1 GiB余量。这些是预算，实际组件占用以现场指标为准。

主库与题库使用同一PostgreSQL集群中的独立数据库和角色，避免重复运行两个数据库实例。题库也可通过受保护的内部接口独立部署；数据库和题库迁出后应重新测量整条请求链路。

## 4. 30GB磁盘规划

| 部分 | 预算GB |
| --- | ---: |
| 系统与原生依赖 | 6 |
| 主库、题库数据与索引 | 8 |
| PostgreSQL本机WAL | 2 |
| 应用、journald与备份日志 | 1 |
| 临时图片、导出、备份暂存 | 3 |
| 程序版本与Web静态资源 | 2 |
| 安全余量 | 8 |

这是规划分配。题库原图默认保留24小时、答案缓存7天；长期加密备份和App安装包放在独立目标，应用主机只保留当前及上一可回滚版本。导出及备份耗时必须满足实际恢复点要求。磁盘70%提醒、80%保护新操作、90%维护，见[数据保留](data-retention.md)。

## 5. 现场验收

分别观察活跃任务、排队任务、SSE连接、查询量、图片体积、管理员报表、认证并发及备份的组合。记录内存RSS/cgroup峰值、CPU、数据库连接/锁/延迟、队列年龄、事件补偿与恢复点。先使用受控上游，再对明确授权的真实账号小规模验证。

队列保存在PostgreSQL；未执行的排队任务和在线客户端不各自占用一条数据库连接。过程记录有界合并，资金与确认事实按事务保留。达到资源保护阈值、出现资金不变量失败或备份目标失效时停止增加负载；平台身份、权限、403/429等异常仍停止相关操作。

当前结果见[本地实现与验收](../development/local-implementation.md)，正式执行决策见[ADR0009](../decisions/0009-native-http-execution.md)。
