<div align="center">

# dbgov-cli

**面向人类与 AI 智能体的「带治理」MySQL & PostgreSQL 数据库操作命令行。**

在护栏下执行查询、改表结构、跑 DML——DML 影响由 `EXPLAIN` 给出预估，变更可预览、全程审计，非空表结构变更还必须先生成经过校验且状态稳定的变更前 DDL 快照。

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

- 📏 **动手前先量影响**——`explain` 与 DML dry-run 报告数据库优化器的行数预估，`schema plan` 报告精确渲染出的 DDL 计划。dbgov 不会用 AI 猜测替代数据库计划；拿不到可用计划就拒绝执行。
- 🛡️ **危险越大,门槛越高**——改一行只需确认;无 `WHERE` 的 `DELETE` 或 `DROP COLUMN` 需要变更工单**外加**一句明确的「是的,允许破坏」。
- 📸 **为受支持的非空表结构变更生成快照**——连续两次读取一致后，dbgov
  才保存经过校验的变更前 DDL；只有能无损表示或保留结构时才允许回滚，否则拒绝执行。
- 👥 **尊重角色**——reader 不能写,writer 不能做破坏性操作,只有 admin 可以。
- 📜 **全部审计**——每次操作(含被拒绝的)都进防篡改日志。
- 🤖 **可放心交给 AI 智能体**——它能自由读取、预览,但**无法**伪造破坏性操作所需的人类审批。

支持 **MySQL** 与 **PostgreSQL**。

---

## ✨ 功能一览

| | |
|---|---|
| 🗄️ **双引擎** | **MySQL** 与 **PostgreSQL**，表结构能力边界因引擎而异。`dbgov capabilities` 是当前构建支持级别的权威来源。 |
| 🔎 **读取与解释** | `query`(只读 SQL,拒绝写入)与 `explain`(真实执行计划 + 预估行数)。 |
| 🧱 **声明式表结构** | `schema list / describe / dump / diff / plan / apply`——把库与目标 `.sql` 对比并只应用差异。 |
| ✏️ **受治理 DML** | `data exec` 执行 `UPDATE`/`DELETE`/`INSERT`,按 `EXPLAIN` 实测影响面做风险分级授权。 |
| 🔄 **表结构 GitOps** | `export` → `import` → `reconcile`(漂移检测 + 可选 `--prune`)→ 从快照 `rollback`。 |
| 🚦 **R0–R3 治理** | 每个操作都做风险分级;受保护上下文整体升一档;AI 调用者永远无法自我授权。 |
| 👥 **RBAC** | 每个上下文的 `reader` / `writer` / `admin` 角色限定写路径能达到的最高风险档。 |
| 📸 **快照与回滚** | 自动保存变更前 DDL 证据；仅在文档声明的引擎边界内自动恢复结构。 |
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

# 3. 查看只读查询计划与优化器行数预估
dbgov explain --sql "SELECT id FROM users WHERE last_seen < '2025-01-01'" -o json

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
| `schema apply` / `import` 删列，或在引擎可无损渲染时改列 | `--allow-destructive` |
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
2. **非空表结构变更先快照。** 新快照会绑定上下文与物理数据库目标；旧的未绑定快照仍可列出，但不能执行回滚。快照始终是变更前 DDL 证据，但只有下文明确支持的引擎结构才能自动回滚；被删的行数据永不恢复。
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

`query` 会拒绝可写 CTE、行锁子句、具有文件 / 会话 / 管理副作用的函数、MySQL 用户变量赋值，以及未知或用户自定义函数调用。MySQL 只允许已识别且未加引号的原生函数；带引号的函数标识符因语义不明确而被拒绝。为防止 `search_path` 和重载遮蔽，普通 PostgreSQL 函数必须使用规范的 `pg_catalog` 限定名（例如 `pg_catalog.count(*)`）；未限定调用只允许 `COALESCE` 等不可重定义的 SQL 语法结构。PostgreSQL 引号标识符按大小写精确匹配，因此 `"pg_catalog"."count"` 可通过，而 `"PG_CATALOG"."count"` 会被拒绝。通过检查的查询会在数据库只读事务中运行，读取完结果后显式回滚。词法分类器无法解析视图函数体、用户自定义运算符，或由隐式 / 显式类型转换间接调用的函数，因此生产上下文仍必须使用数据库只读账号。JSON 会把 SQL `NULL` 保留为 `null`（与 `""` 空字符串不同），表格输出则显示为 `NULL`。
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

> 增量 `schema diff / plan / apply` 只管理解析器明确接受的窄版 `CREATE TABLE` 子集：列名、类型和归一化自增属性。PostgreSQL 支持由此产生的类型 / identity 变更；MySQL 既有列的类型 / 自增变更会 fail-closed，因为无法从该子集无损渲染 `MODIFY COLUMN`。目标 SQL 若包含 default、可空性修饰、键、索引、外键、check、生成列或 identity 选项，会直接拒绝，而不是静默丢掉。PostgreSQL DDL 显式限定到固定的 `public` schema，适用的批次在单个事务中执行。
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

直接使用 `schema plan/apply` 时，简单解析子集支持列级变更；MySQL 原地修改类型或自增属性会被拒绝，因为 `MODIFY COLUMN` 无法保留解析子集未携带的完整列属性。基于导出 DDL 的 GitOps 与 rollback 使用更完整的表定义：不透明定义完全一致时为 no-op，整表缺失时可在 R3 下配合 `--allow-destructive` 原样重建，既有表出现任何不透明差异都会 fail-closed 并交由人工迁移。不透明建表必须是计划中的唯一变更，必须保持对应数据库引擎的规范导出格式，且不能跨引擎复制。

真实 MySQL 的 `SHOW CREATE TABLE` 输出均按不透明定义处理，因此 import/reconcile/rollback 不能原地修改既有 MySQL 表，只支持完全一致的 no-op，或在隔离计划中恢复一张缺失的 InnoDB 表。直接 apply 改动既有 MySQL 表后，快照仍提供经过校验的 DDL 证据，但反向恢复必须由人工审核迁移；非 InnoDB 表会阻断依赖快照的变更。比较时只忽略易变的表级 `AUTO_INCREMENT=<next>` 计数，真正重建仍使用受绑定的原始 DDL。PostgreSQL 表若无法无损重建，snapshot/export 会直接拒绝，包括 serial/identity 或序列驱动列、非系统类型/默认值依赖、注释、独立索引、不支持的约束、分区/继承、非默认外键动作、trigger/policy、自定义存储或非默认 collation。快照只覆盖表结构，不覆盖行数据、trigger、routine、注释或外部序列状态。零语句操作为 R0，且不会写快照。

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

- **已验证发布标签**——仅当 signed annotated tag 经 GitHub 验证，且精确匹配 `package.json`、`CHANGELOG.md` 与最新拉取的 `origin/main` 时才开始发布；CI 与真实数据库集成会在该标签提交上重跑。
- **签名二进制**——每个发布产物都用 [cosign](https://github.com/sigstore/cosign) 无密钥(OIDC)签名;签名的 `checksums.txt` 覆盖全平台。
- **npm provenance**——由 CI 经 OpenID Connect 发布,带 [provenance 溯源声明](https://docs.npmjs.com/generating-provenance-statements),将包与本仓库及工作流关联。
- **校验式安装**——npm postinstall 只信任受 npm provenance 绑定、嵌入 `package.json` 的六个平台 SHA-256 摘要。镜像只能提供二进制字节,不能提供校验数据;校验后的文件会先 fsync,再原子替换旧文件,且不存在跳过校验的开关。
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

MySQL / PostgreSQL 集成测试通过 `DBGOV_TEST_MYSQL_DSN` 与 `DBGOV_TEST_POSTGRES_DSN` 选择性开启。nightly 与发布 CI 使用固定摘要的容器并启用 required 模式，缺少 DSN 时会失败，不会把真实后端测试静默跳过为绿色。详见 [CONTRIBUTING.md](CONTRIBUTING.md) 与安全策略 [SECURITY.md](SECURITY.md)。

dbgov-cli 构建于共享治理引擎 [`opskit-core`](https://github.com/JiangHe12/opskit-core) 之上,是面向 AI 智能体的 **opskit** 治理型 CLI 家族的一员——同族还有 [`srvgov-cli`](https://www.npmjs.com/package/srvgov-cli)(远程服务器)、[`cfgov-cli`](https://www.npmjs.com/package/cfgov-cli)(配置 & Sentinel 规则)与 [`mqgov-cli`](https://www.npmjs.com/package/mqgov-cli)(消息中间件)。

---

## 📄 许可证

[MIT](LICENSE) © JiangHe12
