# dbgov-cli

[English](README.md) | [中文](README_zh.md)

面向 AI agent 和操作人员的 MySQL 与 PostgreSQL 治理 CLI。提供只读查询、schema plan/apply、受治理 DML、GitOps import/reconcile/rollback、审计、RBAC 和本地凭据管理。

## 概览

`dbgov` 围绕治理执行脊椎构建:连接 MySQL 或 PostgreSQL、分类风险、写操作要求显式授权、通过 backend 执行、写结构化审计事件。

## 安装

```bash
npm install -g dbgov-cli
# 或
go install github.com/JiangHe12/dbgov-cli@latest
```

GitHub Releases 提供多平台二进制。npm 安装会下载匹配平台的二进制。

## 快速开始

```bash
DBGOV_PASSWORD='<password>' dbgov-cli ctx set local --engine mysql --host 127.0.0.1 --port 3306 --database app --username appuser -o json
dbgov-cli ctx use local -o json
dbgov-cli query --sql "SELECT 1" -o json
dbgov-cli explain --sql "SELECT * FROM users WHERE id = 1" -o json
dbgov-cli schema list -o json
```

自动化和 AI agent 始终使用 `-o json`。

## 治理模型

| 风险 | 含义 | 授权 |
|---|---|---|
| R0 | 只读操作和本地检查 | 不需要审批,仍审计 |
| R1 | 加列、小影响 WHERE DML、增量 import | `--yes` 或交互确认 |
| R2 | 大影响 WHERE DML 或 protected context 中的 R1 | 非空 `--ticket` + `--yes` |
| R3 | 破坏性 schema、无 WHERE UPDATE/DELETE、prune、破坏性 rollback | `--ticket` + 必需 `--allow-*` + `--yes` |

allow flag 精确按类别:删列/改类型用 `--allow-destructive`,无 WHERE DML 用 `--allow-no-where`,删表 prune 用 `--allow-production-prune`。rollback 至少 R2,并可能同时需要 destructive/prune 两类 allow flag。若 context 设置了 `ticketPattern`,ticket 必须匹配;默认不强制正则。

RBAC 只作用于写路径:`reader` 为 R0,`writer` 最高 R2,`admin` 最高 R3。AI 和自动化绝不能自动填 `--ticket`、`--allow-*` 或高风险 `--yes`。影响面必须来自 `dbgov-cli explain`、`schema plan` 或 `--dry-run`,不能由模型猜测。

所有操作,包括 denied/failed,都会追加到 `~/.dbgov/audit.log`。使用 `audit query`、`audit verify`、`audit prune` 检视、校验和清理轮转日志。

## 常用命令

```bash
dbgov-cli version -o json
dbgov-cli capabilities -o json
dbgov-cli doctor config -o json
dbgov-cli ctx list -o json
dbgov-cli ctx export local > local.ctx.yaml
dbgov-cli ctx import -f local.ctx.yaml --rename local-copy -o json
dbgov-cli query --sql "SELECT * FROM users" -o json
dbgov-cli explain --sql "SELECT * FROM users WHERE active = 1" -o json
dbgov-cli schema dump --dir ./schema -o json
dbgov-cli schema plan -f desired.sql -o json
dbgov-cli schema apply -f desired.sql --dry-run -o json
dbgov-cli data exec --sql "UPDATE users SET active=0 WHERE id=1" --dry-run -o json
dbgov-cli export --dir ./schema -o json
dbgov-cli import ./schema --dry-run -o json
dbgov-cli reconcile ./schema --dry-run -o json
dbgov-cli rollback list -o json
dbgov-cli audit query --since 24h -o json
```

Schema 管理把自增列归一化为跨 MySQL/PostgreSQL 的布尔 `autoIncrement` 模型,覆盖 create、introspect、diff、apply、快照和 rollback。它不保留 PostgreSQL `serial` vs identity、`ALWAYS` vs `BY DEFAULT`、sequence 起始值/步长等细节。

## 配置和 Context

Context 存在 `~/.dbgov`。通过 `ctx set/use/current/list` 管理。凭据可在初始化时使用字面量,也可从 `DBGOV_PASSWORD` 读取,并迁移到安全后端:

```bash
dbgov-cli ctx export prod > prod.ctx.yaml
dbgov-cli ctx import -f prod.ctx.yaml --rename prod-copy -o json
dbgov-cli ctx migrate-credentials --to encrypted-file -o json
dbgov-cli ctx role set prod --target-operator alice --role writer -o json
```

可移植 context 导出默认会脱敏 password。`--include-credentials` 只会在凭据以内联方式存储时导出明文(`plain-yaml` 或空/未设置 credential backend);encrypted-file、keychain、vault 等安全后端凭据必须通过带外方式共享。

CI 中建议设置 `DBGOV_OPERATOR`,让审计和 RBAC 身份稳定。

## Rollback 和快照

schema 变更执行前会捕获变更前 DDL 快照。`rollback --to <snapshot>` 只恢复结构;MySQL/PostgreSQL 中已经因删表/删列丢失的数据不会恢复。dbgov 在 rollback plan 和执行时都会明确提示该有损限制。

## 从源码构建

```bash
go build ./...
go test -count=1 ./...
gofmt -l main.go cmd internal
golangci-lint run --timeout=5m
```

MySQL 与 PostgreSQL 集成测试分别通过 `DBGOV_TEST_MYSQL_DSN`、`DBGOV_TEST_POSTGRES_DSN` 显式启用。

## AI Skill

```bash
dbgov-cli install claude --skills
dbgov-cli install codex --skills
```

## 贡献、安全、许可证

见 [CONTRIBUTING.md](CONTRIBUTING.md)、[SECURITY.md](SECURITY.md)、[LICENSE](LICENSE)。
