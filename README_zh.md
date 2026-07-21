<div align="center">

# dbgov-cli

**面向人类与 AI 智能体的「带治理」MySQL & PostgreSQL 数据库操作命令行。**

在护栏下执行查询、改表结构、跑 DML——每次改动都由 `EXPLAIN` 实测影响、可预览、改前自动快照可回滚、并全程审计,让你和 AI 助手都不会手滑搞挂生产库。

[![npm version](https://img.shields.io/npm/v/dbgov-cli.svg)](https://www.npmjs.com/package/dbgov-cli)
[![CI](https://github.com/JiangHe12/dbgov-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/JiangHe12/dbgov-cli/actions/workflows/ci.yml)
[![license](https://img.shields.io/npm/l/dbgov-cli.svg)](LICENSE)
[![signed](https://img.shields.io/badge/release-cosign%20%2B%20npm%20provenance-blue.svg)](#-供应链可信与校验)

[English](README.md) · [简体中文](README_zh.md)

</div>

---

## 🧭 这是什么?(先看这里)

动生产数据库是运维里最吓人的事之一:漏写一个 `WHERE`、随手一个 `DROP COLUMN`、或一次失败的表结构迁移,几秒钟就能丢数据——通常还没预览、没备份、没记录。把这种权力交给脚本或 AI 智能体,就更吓人了。

**dbgov-cli 给每一个数据库操作都套上护栏。** 把它想成坐在你和数据库之间的一位谨慎 DBA:

- 📏 **动手前先量影响**——`explain`、`schema plan`、`--dry-run` 精确告诉你一次改动会命中多少行、哪些列。dbgov 绝不猜;量不出就拒绝执行。
- 🛡️ **危险越大,门槛越高**——改一行只需确认;无 `WHERE` 的 `DELETE` 或 `DROP COLUMN` 需要变更工单**外加**一句明确的「是的,允许破坏」。
- 📸 **每次变更前先快照表结构**——出问题可把结构回滚。
- 👥 **尊重角色**——reader 不能写,writer 不能做破坏性操作,只有 admin 可以。
- 📜 **全部审计**——每次操作(含被拒绝的)都进防篡改日志。
- 🤖 **可放心交给 AI 智能体**——它能自由读取、预览,但**无法**伪造破坏性操作所需的人类审批。

支持 **MySQL** 与 **PostgreSQL**。

---

## ✨ 功能一览

| | |
|---|---|
| 🗄️ **双引擎** | **MySQL** 与 **PostgreSQL**,功能对等。`dbgov capabilities` 报告当前构建支持什么。 |
| 🔎 **读取与解释** | `query`(只读 SQL,拒绝写入)与 `explain`(真实执行计划 + 预估行数)。 |
| 🧱 **声明式表结构** | `schema list / describe / dump / diff / plan / apply`——把库与目标 `.sql` 对比并只应用差异。 |
| ✏️ **受治理 DML** | `data exec` 执行 `UPDATE`/`DELETE`/`INSERT`,按 `EXPLAIN` 实测影响面做风险分级授权。 |
| 🔄 **表结构 GitOps** | `export` → `import` → `reconcile`(漂移检测 + 可选 `--prune`)→ 从快照 `rollback`。 |
| 🚦 **R0–R3 治理** | 每个操作都做风险分级;受保护上下文整体升一档;AI 调用者永远无法自我授权。 |
| 👥 **RBAC** | 每个上下文的 `reader` / `writer` / `admin` 角色限定写路径能达到的最高风险档。 |
| 📸 **快照与回滚** | 变更前自动快照表结构;结构级恢复,并明确告知数据丢失风险。 |
| 📜 **防篡改审计** | 每个操作写入可哈希校验的日志;`audit verify` 检测篡改。 |
| 🔏 **可信供应链** | 二进制经 **cosign 签名**、npm 带 **provenance**、安装器校验 **SHA-256**。 |

---

## 📦 安装

```bash
npm install -g dbgov-cli
```

这会装一个很小的启动器;首次运行时,它会从已签名的 [GitHub Release](https://github.com/JiangHe12/dbgov-cli/releases) 下载对应你 OS/架构的预编译二进制,并在使用前**校验 SHA-256**。安装器需要 Node.js ≥ 14(CLI 本身是自包含的 Go 二进制)。

<details>
<summary>其它安装方式</summary>

- **直接下载**——从 [Releases 页面](https://github.com/JiangHe12/dbgov-cli/releases)取二进制,用 cosign 签名的 `checksums.txt` 校验,放进 `PATH` 并重命名为 `dbgov`。
- **从源码**——`go install github.com/JiangHe12/dbgov-cli@latest`(Go 1.25+)。

```bash
dbgov version
dbgov doctor config -o json     # 静态 + 只读诊断
```

</details>

---

## 🚀 快速上手(60 秒)

```bash
# 1. 把 dbgov 指向你的数据库(保存为可复用的「上下文」;密码不写入 YAML)
dbgov ctx set prod --engine mysql \
  --host 127.0.0.1 --port 3306 --database app --username appuser --env prod --protected --dry-run
dbgov ctx set prod --engine mysql \
  --host 127.0.0.1 --port 3306 --database app --username appuser --env prod --protected \
  --ticket <human-ticket> --allow-context-change --yes
dbgov ctx use prod --ticket <human-ticket> --allow-context-change --yes
export DBGOV_PASSWORD='***'   # context 未保存凭据时,连接命令会读取它

# 2. 读——只读 SQL 免费(R0)且拒绝写入
dbgov query --sql "SELECT id, name FROM users LIMIT 10" -o json

# 3. 改之前先量——看计划与预估行数
dbgov explain --sql "UPDATE users SET active = 0 WHERE last_seen < '2025-01-01'" -o json

# 4. 预览一次 DML(此时什么都不会执行)
dbgov data exec --sql "UPDATE users SET active = 0 WHERE id = 42" --dry-run -o json

# 5. 应用——小范围写入(R1)只需你确认
dbgov data exec --sql "UPDATE users SET active = 0 WHERE id = 42" --yes -o json

# 6. 看看刚才发生了什么
dbgov audit query --since 1h -o json
```

> 💡 **提示:** 创建生产上下文时加 `--protected`,之后 dbgov 会自动把该上下文里每个操作升一档。

---

## 🔐 治理模型(最重要的部分)

每条命令都会被归入一个**风险档位**,越危险需要的人类授权越明确:

| 档位 | 涵盖范围 | 你必须提供 |
|:---:|---|---|
| **R0** | 读取与查看(`query`、`explain`、`schema list/describe/dump/diff/plan`、`audit`、`doctor`) | 无——但仍会被审计 |
| **R1** | 小范围安全写入(加列、带 `WHERE` 且预估影响小的 `data exec`) | `--yes`(或交互确认) |
| **R2** | 升级写入(`EXPLAIN` 行数超阈值的 `data exec`;受保护上下文里的任何 R1) | `--yes` **加** 非空 `--ticket` |
| **R3** | 破坏性操作和治理控制面变更(替换/删除上下文、迁移凭据、修改角色) | 以上**再加**精确的 `--allow-*` 标志 |

**R3 allow 标志**——破坏从不隐式发生:

| 操作 | 所需标志 |
|---|---|
| `schema apply` / `import` 删列或改列 | `--allow-destructive` |
| `data exec` 无 `WHERE` 的 `UPDATE`/`DELETE` | `--allow-no-where` |
| `reconcile --prune` 删表 | `--allow-production-prune` |
| `rollback --to` 涉及删列 / 删表 | `--allow-destructive` / `--allow-production-prune` |
| `ctx set` / `ctx use` / `ctx import` / `ctx migrate-credentials` | `--allow-context-change` |
| `ctx delete` | `--allow-context-delete` |
| `ctx role set` / `ctx role unset` | `--allow-role-change` |
| 确认执行的 `audit prune` | `--allow-audit-prune` |

**RBAC**(上下文配置了角色时):`reader` → 最高 R0,`writer` → 最高 R2,`admin` → 最高 R3。治理控制面写入始终按目标的**变更前策略**授权;新上下文使用持久化的 current context 策略,没有 current context 的首次引导也仍需 R3 授权。

授权与审计身份始终来自可信本机 OS 身份 `username@hostname`;已弃用的全局 `--operator` 覆盖和 `DBGOV_OPERATOR` 都会被忽略。这能阻止命令参数或环境变量冒充其他角色,但无法区分同一 OS 账号下运行的人类进程与 AI 进程。在接入外部签名审批源或让自动化使用独立受保护 OS 账号之前,本地 RBAC 不能作为同账号人类与 AI 之间的安全边界。

三条原则保证安全——尤其对自动化:

1. **影响来自数据库,而非猜测。** 用 `explain` / `schema plan` / `--dry-run`;dbgov 宁可 fail-closed 也不估算。受治理的 `UPDATE` / `DELETE` 在真正改数据前，会在同一事务内重新校验精确的 EXPLAIN 指纹与预估行数。
2. **变更先快照。** 新快照会绑定上下文与物理数据库目标；旧的未绑定快照仍可列出，但不能执行回滚。回滚只恢复*结构*——被删的行数据永不恢复(dbgov 会大声警告，并返回 `dataRestored: false`)。
3. **🤖 AI 智能体绝不能伪造 `--ticket`、`--allow-*` 或高风险 `--yes`。** 它们是*人类*授权输入;智能体应上报「这步需要审批 X」然后停下。

---

## 📚 命令参考

`dbgov <命令> [标志]`。加 `-o json` 得机器可读输出,任意命令加 `--help` 看完整标志,`dbgov capabilities -o json` 看支持的引擎/特性。

<details open>
<summary><b>读取与解释</b></summary>

```bash
dbgov query   --sql "SELECT ..." -o json          # 只读;拒绝写入(R0)
dbgov explain --sql "SELECT ..." -o json          # 执行计划 + 预估行数(R0)
```

`query` 会拒绝可写 CTE、行锁子句、具有文件 / 会话 / 管理副作用的函数，以及 MySQL 用户变量赋值。通过检查的查询会在数据库只读事务中运行，读取完结果后显式回滚。JSON 会把 SQL `NULL` 保留为 `null`（与 `""` 空字符串不同），表格输出则显示为 `NULL`。自定义函数和视图无法仅靠词法分析证明无副作用，因此生产上下文仍必须使用数据库只读账号。
</details>

<details>
<summary><b>表结构 schema</b> — 查看、对比、应用 DDL</summary>

```bash
dbgov schema list                       -o json   # R0
dbgov schema describe <table>           -o json   # R0
dbgov schema dump                         -o json   # R0（标准输出）
dbgov schema dump  --dir ./schema --yes -o json   # R1（写本地文件）
dbgov schema diff  -f desired.sql       -o json   # R0
dbgov schema plan  -f desired.sql       -o json   # R0 —— plan 的风险/告警视为权威
dbgov schema apply -f desired.sql --dry-run -o json
dbgov schema apply -f desired.sql --yes                                  -o json   # R1(增量)
dbgov schema apply -f desired.sql --ticket DB-123 --allow-destructive --yes -o json # R3(破坏性)
```

> 自增列在两种引擎上以归一化的布尔模型表示;create / diff / apply / snapshot / rollback 行为保留,但 PostgreSQL 的 `serial` 与 identity、`ALWAYS` 与 `BY DEFAULT`、序列 start/increment 选项**有意不保留**。生成的 PostgreSQL 表 DDL 会显式限定到固定的 `public` schema，适用的 DDL 批次在单个事务中执行。
</details>

<details>
<summary><b>受治理 DML</b> — <code>data exec</code></summary>

```bash
dbgov data exec --sql "UPDATE ... WHERE ..." --dry-run -o json     # 预览影响 + 所需授权
dbgov data exec --sql "UPDATE ... WHERE id = 42" --yes  -o json     # R1 小影响
dbgov data exec --sql "UPDATE ... WHERE <宽条件>" --ticket DB-123 --yes -o json        # R2 大影响
dbgov data exec --sql "DELETE FROM sessions"    --ticket DB-123 --allow-no-where --yes -o json  # R3
dbgov data exec -f change.sql --dry-run -o json                     # 从文件读 DML
```
</details>

<details>
<summary><b>表结构 GitOps</b> — export · import · reconcile · rollback</summary>

```bash
dbgov export --dir ./schema --yes -o json                         # R1；导出当前表结构到文件
dbgov import ./schema --dry-run -o json
dbgov import ./schema --yes -o json                               # R1 / 含破坏性则 R3
dbgov reconcile ./schema --dry-run -o json                        # 检测漂移
dbgov reconcile ./schema --yes -o json
dbgov reconcile ./schema --prune --ticket DB-123 --allow-production-prune --yes -o json  # R3 prune
dbgov rollback list -o json                                       # 列出变更前快照
dbgov rollback --to <snapshot-id> --dry-run -o json
dbgov rollback --to <snapshot-id> --ticket DB-123 --yes -o json   # 仅结构;数据不恢复
```

回滚 dry-run 返回带计划/目标指纹的 `SchemaPlan`；成功执行返回 `RollbackResult`，其中包含计划/已应用语句数、`scope: "schema-structure"` 与 `dataRestored: false`。
</details>

<details>
<summary><b>上下文、角色、审计与诊断</b></summary>

```bash
# 上下文(MySQL 或 PostgreSQL)
dbgov ctx set <name> --engine mysql|postgres --host <h> --port <p> --database <db> --username <u> [--protected] --dry-run
dbgov ctx set <name> ... --ticket <human-ticket> --allow-context-change --yes
dbgov ctx set <name> --credential-backend keychain|encrypted-file --password <secret> --ticket <human-ticket> --allow-context-change --yes
dbgov ctx list|current
dbgov ctx use <name> --dry-run
dbgov ctx use <name> --ticket <human-ticket> --allow-context-change --yes
dbgov ctx delete <name> --dry-run
dbgov ctx delete <name> --ticket <human-ticket> --allow-context-delete --yes
dbgov ctx export <name> [--include-credentials] -o json
dbgov ctx import -f ctx.yaml [--rename <new>] [--force] --dry-run -o json
dbgov ctx migrate-credentials --to encrypted-file|keychain [--context <name>] --dry-run -o json

# RBAC(仅写路径):reader → R0,writer → R2,admin → R3
dbgov ctx role set <context> --target-operator <os-user@hostname> --role writer --dry-run -o json
dbgov ctx role set <context> --target-operator <os-user@hostname> --role writer --ticket <human-ticket> --allow-role-change --yes -o json
dbgov ctx role list <context> -o json

# 审计(防篡改;prune 只删轮转日志)
dbgov audit query  [--since 24h] [--risk R2] [--limit 50] -o json
dbgov audit verify -o json
dbgov audit prune  (--before <30d|YYYY-MM-DD> | --keep-last <n>) -o json
dbgov audit prune  (--before <30d|YYYY-MM-DD> | --keep-last <n>) --confirm --yes --ticket <人工工单> --allow-audit-prune -o json

# 诊断与生态
dbgov doctor config|network|auth -o json
dbgov capabilities -o json
dbgov completion bash|zsh|fish|powershell
dbgov install <agent> --skills --yes # R1；安装 dbgov AI 技能(claude、codex …)
dbgov version
```

> `audit prune` 只删同目录下严格命名为 `<active>.YYYYMMDD-HHMMSS[.<正整数序号>].log` 的**轮转**日志(绝不删活动 `audit.log`),并默认 dry-run。确认执行的 prune 是固定 R3 的证据销毁操作,必须同时提供 `--confirm`、`--yes`、非空 `--ticket` 和精确的 `--allow-audit-prune`。授权使用持久化 current context 策略(没有 current 时为空策略),`--context` 不能替换该策略。dry-run 在授权前返回且不删除文件。确认 prune 会在上下文配置锁内重新加载策略，并将该锁一直持有到 intent、删除和 outcome 完成；控制 intent/outcome 写入 sibling `.<audit-base>-control`，绝不进入目标证据命名空间。随后 audit core v2 在审计路径锁内绑定完整的预览轮转集合，验证认证链和稳定文件身份，推进 checkpoint，并耐久删除选中的最老前缀。策略、候选、身份或证据发生变化时都会 fail-closed；成功的 JSON 输出会报告最终 `checkpointState`。CI 身份来自其 OS 用户与主机名;`DBGOV_OPERATOR` 不能覆盖身份。

每个真实变更都会在校验与授权完成后、首个目标副作用前写入 `dbgov-cli.io/mutation-audit/v1` intent，执行后再写入具有相同 `mutationId` 的 outcome。intent 写入失败会阻断变更。dbgov 以 audit core v2 返回的耐久提交状态为准：只有已确认未提交的 outcome 才会进入审计日志相邻、仅所有者可访问的 `<audit.log>.outcome-spool`；已确认提交或提交状态不确定的 outcome 都不会入队，因为记录可能已经存在。若重放本身变为状态不确定，该条目会被原子重命名并追加 `.indeterminate`，后续重放在人工核对审计记录前一律失败关闭。可重试队列会在下一条 intent 前按序重放，消费方应按 `(mutationId, phase)` 去重；批量结果只记录有界的成功/失败/跳过计数。

持久化审计与遥测不会保存原始 ticket、reason、SQL、审计目标中的数据库名/对象值、后端错误文本、body 或命令输出。审计仅保存域隔离的 SHA-256 指纹与字节长度或有界计数；`audit query` 返回历史记录前也会执行相同清洗。

非交互运行优先使用 `DBGOV_PASSWORD`;当命令建立数据库连接且所选 context 没有保存凭据时会读取它。若要通过 `ctx set` 持久保存密码,`--password` 必须配 `--credential-backend keychain` 或 `--credential-backend encrypted-file`;plain-yaml 的 `ctx set --password` 会被拒绝。legacy/import 进来的 inline 凭据仍可读取,用于迁移与导出兼容。
</details>

---

## 🤖 给 AI 智能体

- 先跑 `dbgov capabilities -o json`——它是支持引擎与特性的权威来源。
- 处处用 `-o json`;每条命令返回稳定、带版本的信封结构。
- 影响范围取自 `explain` / `schema plan` / `--dry-run`,**绝不**靠自己推理。
- **绝不自我填入 `--ticket`、`--allow-*` 或高风险 `--yes`。** 把所需的人类审批上报,然后停下。

```bash
dbgov install claude --skills --yes # 也支持:codex、opencode、copilot、cursor、windsurf、aider、cc-switch
```

---

## 🔏 供应链可信与校验

- **签名二进制**——每个发布产物都用 [cosign](https://github.com/sigstore/cosign) 无密钥(OIDC)签名;签名的 `checksums.txt` 覆盖全平台。
- **npm provenance**——由 CI 经 OpenID Connect 发布,带 [provenance 溯源声明](https://docs.npmjs.com/generating-provenance-statements),将包与本仓库及工作流关联。
- **校验式安装**——npm postinstall 在安装前对照签名的 `checksums.txt` 校验二进制 SHA-256。
- **防篡改审计**——`dbgov audit verify` 重走日志并报告任何断裂或改动。

---

## 🏗️ 从源码构建与贡献

```bash
git clone https://github.com/JiangHe12/dbgov-cli && cd dbgov-cli
go build ./...
go test -count=1 ./...
gofmt -l main.go cmd internal      # 必须无输出
golangci-lint run --timeout=5m
go vet -tags=integration ./...
npm pack --dry-run
```

MySQL / PostgreSQL 集成测试通过 `DBGOV_TEST_MYSQL_DSN` 与 `DBGOV_TEST_POSTGRES_DSN` 选择性开启。详见 [CONTRIBUTING.md](CONTRIBUTING.md) 与安全策略 [SECURITY.md](SECURITY.md)。

dbgov-cli 构建于共享治理引擎 [`opskit-core`](https://github.com/JiangHe12/opskit-core) 之上,是面向 AI 智能体的 **opskit** 治理型 CLI 家族的一员——同族还有 [`srvgov-cli`](https://www.npmjs.com/package/srvgov-cli)(远程服务器)、[`cfgov-cli`](https://www.npmjs.com/package/cfgov-cli)(配置 & Sentinel 规则)与 [`mqgov-cli`](https://www.npmjs.com/package/mqgov-cli)(消息中间件)。

---

## 📄 许可证

[MIT](LICENSE) © JiangHe12
