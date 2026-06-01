# dbgov 设计文档

> 状态:设计阶段(尚未实现)。本文件记录 dbgov 的定位、范围、治理模型与命令树,供后续实现与给 codex 出落地指令时对齐。
> 关联产品线:`nacos-cli`(配置治理)、`sentinel-cli`(流控规则治理)、`dbgov`(关系型库治理)。三者共享同一套治理 DNA,未来抽公共核心模块 `opskit-core` 复用。

---

## 1. 定位

dbgov 是**面向 AI Agent 的、受治理的关系型数据库操作 CLI**。

它**不是**交互式 SQL 客户端(不与 `mysql`/`psql`/`mycli`/`mysqlsh` 在 REPL、补全、高亮、分页上竞争),也**不是**版本化迁移引擎(不与 Flyway/Liquibase 抢版本迁移史的活)。

它是 **AI 操作关系库的唯一受治理入口**:每一个操作——哪怕只是一次读——都流经统一的治理管线(风险分级、dry-run、工单门禁、RBAC、审计)。**没有绕过治理直接执行的后门。**

### 1.1 决策尺子

判断某能力是否值得做,看:**"有没有让 AI 友好使用的 CLI"**,但要**按具体任务 + 该任务危险程度**衡量,且"友好"对高风险任务必须包含"安全 + 结构化"语义。

- `mysql`/`psql` 对"敲个查询"友好,但对"安全地改生产 schema"**不友好**(无 dry-run / 无影响面预估 / 无回滚 / 无多环境 context / 无审计)→ 缺口在此 → dbgov 成立。
- Redis 因 `redis-cli` 已对 AI 够友好、治理面薄,故**不单独做**(以后若需要,作为 `opskit-core` 上的薄 profile,且不得命名为 `redis-cli`——撞官方二进制)。

---

## 2. 命名

| 项 | 值 |
|---|---|
| 命令(binary) | `dbgov` |
| npm 包 / 仓库 | `dbgov-cli` |
| `package.json` bin | `"bin": { "dbgov": "bin/dbgov.js" }` |

命名原则:按"价值/治理"命名,不按引擎(`mysql-cli`)或接口(`sql-cli`)命名,避免被当成"又一个客户端"并撞名。npm 上 `dbgov`、`dbgov-cli` 均未占用(2026-05 核验)。

---

## 3. 范围边界

### 3.1 纳入(IN)

- 受治理的**连接 + 多环境 context**(直连 / TLS / SSH 隧道 / 云)
- **读 / 自省**(只读查询、schema 导出、diff、EXPLAIN、影响面预估)
- **schema 变更**(声明式)
- **数据变更**(DML,命令式)
- **GitOps**:schema export / import / reconcile / rollback
- 治理横切:风险分级、dry-run、工单门禁、RBAC、追加式审计(append-only)、JSON 契约、OTel

### 3.2 排除(OUT,刻意不做)

- 交互式客户端体验(REPL、自动补全、语法高亮、分页美化)——交给 mycli/mysqlsh
- 完整的版本化迁移历史框架——**与 Flyway/Liquibase 互操作而非取代**
- 自研在线大表 DDL 引擎——**外包 gh-ost / pt-osc**(MySQL),PG 用原生事务性 DDL
- NoSQL(Redis / ES / Mongo 等)——治理原语是 SQL/DDL 特有的,不进来

### 3.3 一期(v1)不做、留接口

- **账号 / 权限治理**(`CREATE USER` / `GRANT` / `REVOKE` / role,即 DCL):一期不做,留接口,后续作为独立模块加入。理由:它是另一个子领域、各引擎模型差异大、且不是最痛的点;先把 schema + 数据变更这个又痛又高价值的核心做扎实。

---

## 4. 引擎与多后端

**形态:一个 CLI、多引擎 backend**(复用 sentinel-cli 的 `backend.Backend` 接口模式),引擎是 context 里的一个字段。

| 引擎家族 | 成员 | 接入成本 |
|---|---|---|
| **MySQL 家族** | MySQL、MariaDB、Percona、TiDB、PolarDB、OceanBase、Aurora-MySQL | ≈ 一个 backend(同 wire 协议/方言) |
| **PostgreSQL 家族** | PostgreSQL、CockroachDB、YugabyteDB、Aurora-PG | 第二个 backend(不同方言/EXPLAIN/驱动) |
| Oracle / SQL Server | —— | 重、靠后或按需,不在首批 |

**重要:安全/回滚能力按 backend 声明,不一刀切。**

- **PostgreSQL 的 DDL 是事务性的**:`ALTER` 可包在事务里,失败 `ROLLBACK` 真回滚 → PG backend 可承诺"DDL 失败自动回滚"。
- **MySQL 的 DDL 触发隐式提交、不可回滚** → MySQL backend 只能承诺"dry-run + 影响行数预估 + 在线 DDL 外包",不能承诺 DDL 回滚。

---

## 5. 能力分层

| 层 | 能力 | 风险 / 自主度 |
|---|---|---|
| **L0 连接 & 上下文** | ctx 多环境、按引擎选 backend、连接方式(直连/TLS/SSH 隧道/云)、凭据(credstore)、protected/ticket/RBAC 配置;doctor 诊断;capabilities 发现 | 配置类 |
| **L1 读 & 自省** | 只读查询(SELECT/SHOW)、EXPLAIN、schema 导出(表/索引/FK/DDL)、schema diff、变更计划 / dry-run、影响行数预估 | **R0,AI 全自主**(仍审计) |
| **L2 变更管理** | 数据 DML(INSERT/UPDATE/DELETE)、schema DDL、export/import/reconcile、rollback | **R1–R3,治理门禁** |
| **L3 治理(横切所有层)** | 风险分级 R0–R3、ticket、allow-flags、RBAC、追加式审计(append-only)、dry-run、JSON 契约、OTel | 贯穿全部 |

L3 不是一个"功能",而是所有 L1/L2 操作**必经的管线**——这是 dbgov 区别于裸客户端的根本。

---

## 6. 核心 UX 闭环(对话式)

目标:用户用口语跟 AI 沟通,AI 在使用 dbgov 时自动设计 SQL,附解释 + 影响说明,用户同意后执行。

```
① 用户:口语说意图            "帮订单表加个备注字段"
② AI:设计出 SQL              ALTER TABLE orders ADD COLUMN remark VARCHAR(255)
③ dbgov:dry-run 出权威计划    解释 + 影响(影响行数/破坏性) + 风险等级 + 需要的授权
④ AI:用大白话讲给用户         "给 orders 加 remark 字段,影响 0 行数据,低风险(R1)"
⑤ 用户:同意
⑥ dbgov:执行 + 写审计
```

### 6.1 "解释 + 影响说明"必须拆成两半,可信度不同

| | 谁产出 | 性质 |
|---|---|---|
| **解释(我要干嘛)** | **AI** 用大白话讲意图 | 沟通用 |
| **影响说明(实际会怎样)** | **必须来自 dbgov 的 dry-run** | 权威事实:真实影响行数(EXPLAIN)、是否破坏性、风险等级、需要的授权 |

**绝不能让 AI 自己"猜"影响。** AI 编的影响数字是幻觉,不能作批准依据。dbgov 的核心价值正是:用真实数据库算出**权威**影响,让那句"影响说明"可信。闭环里 ④ 的影响数字来自 ③ 的 dbgov,不是 AI 编的。

---

## 7. 治理模型

沿用 nacos-cli / sentinel-cli 的 R0–R3 模型与授权语义。

### 7.1 风险分级

| 风险 | 含义 | dbgov 典型操作 |
|---|---|---|
| R0 | 只读 / 本地 | query(只读)、schema dump/diff/describe、explain、plan、audit query |
| R1 | 普通写 | 增量 DDL(加字段/加索引)、带 WHERE 且影响面小的 DML |
| R2 | 敏感写 / 受保护环境的 R1 | 受保护环境的写、需要工单的操作 |
| R3 | 破坏性 / 不可逆 / 受保护环境的 R2 | DROP COLUMN/TABLE、无 WHERE 的 UPDATE/DELETE、TRUNCATE、reconcile --prune、生产环境破坏性变更 |

### 7.2 dbgov 特有的风险判定输入

普通 CLI 只看操作类型;dbgov 还要吃:

- 语句类别(DQL / DML / DDL / DCL / TCL)
- **`UPDATE`/`DELETE` 是否带 `WHERE`**:无 WHERE → 强制拉到 R3
- **EXPLAIN 估算影响行数** → 爆炸半径分级
- **DDL 性质**:增量(ADD COLUMN/INDEX,R1)vs 破坏性(DROP/MODIFY/TRUNCATE,R3)
- **声明式 diff 中的破坏性条目**(见 §8.1)→ 强制标红 + 拉高风险

### 7.3 授权墙("用户同意"分两档)

- **R1**:用户口头同意 → AI 带 `--yes` 执行,顺滑。
- **R2/R3**:口头不够,需 **`--ticket`**(R3 还需对应 `--allow-*` flag)。**`--ticket` / `--allow-*` 是 AI 填不了的墙,必须由人提供**——"聊天里随口 yes"恰是最易酿成生产事故的批准方式,必须逼出一次刻意、可追溯的授权。
- 墙卡在哪、卡多严,是可配旋钮(默认建议:R1 口头即可,R2 起必须 ticket;`--protected` 上下文整体升一级)。

**绝不允许 AI 代填 `--ticket` / `--allow-*` / `--yes`(高风险)。** 授权缺失时报错并把缺口暴露给用户。

### 7.4 其他治理横切

- **审计**:每个操作(含读)写**追加式(append-only)JSONL 审计**;`dbgov audit verify` 做 JSON 合法性 / schema / 时间戳时序校验 + 坏行隔离修复(quarantine/repair),并支持**可选 age 加密**保护机密性(参照 sentinel `verify.go`);`dbgov audit query` 检索。**注:当前实现不是哈希链 / 逐条签名式的强防篡改**——能写文件者仍可改记录而不被 verify 检出;若 dbgov 真需要不可抵赖的完整性保证,须后续单独在 opskit-core 引入哈希链 / 签名机制(届时三工具共享)。
- **RBAC**:context 内 per-operator 角色(reader/writer/admin → R0/R2/R3 上限)。
- **JSON 契约**:`apiVersion` + `kind` 信封,供 AI 程序化消费;`--dry-run -o json` 产出权威计划。
- **OTel**:每条命令一个 span,默认脱敏。

---

## 8. schema 变更:声明式(报目的地)

dbgov 改表结构走**声明式**:用户/AI 写"期望的最终表结构",dbgov 拿期望态与数据库现状对比,自动算出 `ALTER`,dry-run 给人看,批准后执行。

- 与现有套件一致:nacos/sentinel 本就是"文件放期望态 → diff 看差异 → reconcile 落地"。
- **不**自建版本化迁移脚本引擎;与 Flyway/Liquibase 互操作。
- 在线大表 DDL 外包 gh-ost / pt-osc(MySQL)/ 原生事务 DDL(PG)。

### 8.1 声明式的已知危险:破坏性 / 歧义 diff(治理发力点)

声明式自动算 diff 会产生**自动生成的破坏性变更**,必须重点治理:

- 期望态比现状少了列 → 自动算出 `DROP COLUMN`,**数据不可逆丢失**。
- 列改名在声明式里会被误判为 `drop + add`(rename 歧义)。

**要求**:dry-run 必须把破坏性条目(DROP / 数据丢失 / 疑似 rename)**大声标红、强制拉高风险等级、走 ticket + allow-flag**。这是 dbgov 比裸 Atlas/Skeema 多出来的价值——"工具自动算 diff"很方便,但破坏性 diff 必须有治理兜底。

---

## 9. 数据变更:命令式(DML)+ 治理

数据增删改无法声明式(不会去声明"全表所有行该长啥样"),走**命令式**:AI 直接给真实 `INSERT/UPDATE/DELETE` 语句,dbgov 执行,但包上治理:dry-run + 影响行数预估 + 无 WHERE 拦截 + 风险分级 + 工单门禁 + 审计 + (可行时)事务包裹。

---

## 10. 命令树(草案,标注风险等级)

> 约定:`-o json` 为 AI 消费默认;写操作均支持 `--dry-run`;R2+ 需 `--ticket`,R3 需对应 `--allow-*`。

### 10.1 上下文 & 连接(L0)

```
dbgov ctx set <name> --engine mysql|postgres --host H --port P [--tls ...] [--ssh-tunnel ...] [--protected] [--ticket-pattern ...]
dbgov ctx use <name>
dbgov ctx list | current | delete <name>
dbgov ctx migrate <name> --to keychain|encrypted-file|plain|vault     # 凭据后端迁移
dbgov ctx role set|unset|list <name> --operator ... --role reader|writer|admin   # RBAC, 写角色为 R1+
```

### 10.2 诊断 & 元信息

```
dbgov doctor [config|network|auth|permissions]    # R0(permissions 探针为 R1 写探测)
dbgov capabilities                                  # R0
dbgov version                                       # R0
```

### 10.3 读 & 自省(L1,R0)

```
dbgov query --sql "SELECT ..."          # 只读;拒绝写语句;R0,审计
dbgov explain --sql "..."               # 计划/爆炸半径;R0
dbgov schema dump [--dir ./schema]      # 导出现状为期望态文件;R0
dbgov schema list | describe <table>    # R0
dbgov schema diff -f desired.sql        # 期望态 vs 现状,只读计算;R0
dbgov schema plan -f desired.sql        # = diff + dry-run 权威影响计划;R0
```

### 10.4 schema 变更(L2,声明式)

```
dbgov schema apply -f desired.sql [--dry-run] [--ticket ...] [--allow-destructive]
#   增量(加字段/索引)→ R1
#   破坏性(DROP/改类型/疑似 rename)→ R3,需 --ticket + --allow-destructive
#   --protected 上下文整体升一级
```

### 10.5 数据变更(L2,命令式 DML)

```
dbgov data exec --sql "UPDATE ... WHERE ..." [--dry-run] [--ticket ...] [--allow-no-where]
#   带 WHERE 且影响面小 → R1
#   无 WHERE / 影响面巨大 → R3,需 --ticket + --allow-no-where
```

### 10.6 GitOps 批量(L2)

```
dbgov export --dir ./schema                  # R0
dbgov import ./schema [--dry-run] [--ticket]  # R1+
dbgov reconcile ./schema [--prune] [--dry-run] [--ticket] [--allow-production-prune]   # prune → R3
dbgov rollback (--to <snapshot>|--ticket ...) # R2+;能力按 backend 声明(PG 真回滚 / MySQL 限于 schema 快照)
```

### 10.7 审计(L3)

```
dbgov audit query [--since ...] [--operator ...] [--type ...]   # R0
dbgov audit verify [--strict]                                    # R0
```

### 10.8 预留(v1 不实现,仅占位接口)

```
dbgov grant ... / dbgov user ...    # 账号/权限治理(DCL),一期不做
```

---

## 11. 与现有工具的关系 / 未来

- dbgov 与 nacos-cli / sentinel-cli **共享治理外壳**(safety/audit/credstore/ctx/backup/printer/apperrors + RBAC + OTel + backend 接口 + 追加式审计(append-only) + pkg 对外 API)。
- 这套外壳目前在 nacos-cli / sentinel-cli 间是**复制 + 已分叉**的。建议在做 dbgov 之前(2→3 的时间窗口)**抽出共享模块 `opskit-core`**,三者 import 之,各自只留领域 backend + 命令 + 校验。
- 未来若做 Redis/ES 治理,作为 `opskit-core` 上的独立领域工具(品牌名,非 `redis-cli`)。

---

## 12. 已决策(原开放问题)

### Q1 授权墙默认值 + protected 升级粒度
**不降低 ticket 墙,靠把 R1 分准来换顺滑。** 沿用 R0 自由 / R1 口头 yes(AI 带 `--yes`)/ R2 需 ticket / R3 需 ticket + allow。对话式顺滑靠"把真正安全的日常操作准确归到 R1",而非削弱 R2。protected(生产)整体升一级(R1→R2)保留——生产里加字段也要 ticket,是特性不是摩擦。爆炸半径阈值 per-context 可配:无 WHERE → 恒 R3;带 WHERE 但预估影响行数 > 阈值(默认 1000)→ 升 R2/R3。

### Q2 声明式期望态文件格式
**纯 SQL DDL(每引擎原生),不做中性 DSL。** AI 写原生 DDL 最可靠;跨引擎可移植是假设性需求(§2.2 不为假设需求做抽象);backend 各自负责"解析本引擎 DDL → 自省现状 → 算 diff"。将来真要跨引擎,再在原生 DDL 之上加一层。

### Q3 MySQL rollback 边界
**按 backend 老实声明能回滚什么,绝不给假安慰。** PG backend:事务内真回滚 + 旧 schema 快照;数据一般不可逆。MySQL backend:无 DDL 回滚(隐式提交),只能存"变更前 schema DDL 快照"→ 能重建结构但删掉的数据回不来(结构级、有损);数据不可逆。`capabilities` 显式暴露每 backend 的 rollback 能力,dry-run 在"无法回滚"时大声说明。可选增强:数据 DML 的有界 undo 预镜像(`--capture-undo`,超行数上限则拒绝)。不盲目复用 nacos/sentinel 的"存旧值"备份模型。

### Q4 在线 DDL 外包(gh-ost/pt-osc)集成
**当 schema-apply 背后的可插拔执行器,dbgov 只做门禁+审计+dry-run 编排+可观测,不重造。** 小表/快操作 → 直接 ALTER;大表/锁表 → 委托在线工具。context 声明 `--online-ddl-tool gh-ost|pt-osc|none` + 路径/参数。dbgov 负责风险分级、ticket、dry-run(展示将执行命令 + 预估耗时/影响)、审计(记录完整调用与结果)、进度结构化透传。**分期**:v1 = 直接 DDL + 大表/锁表硬拦截(超阈值报错提示配置在线工具);在线 DDL 委托放 v1.1。

### Q5 opskit-core 抽取清单 + 分叉对齐
**先抽 opskit-core,再建 dbgov;以 sentinel 代码为基底。**
- 进 core:safety(risk/ticket/RBAC/authorize)、audit(append-only + schema/时序 verify + 坏行隔离 + 可选 age 加密)、credstore、ctx 框架、backup 框架、printer、apperrors、lockfile、输出信封(apiVersion/kind)、`backend.Backend` 接口骨架、doctor 框架、capabilities 框架、OTel。
- 留各 CLI:领域 backend、领域命令、领域校验、SKILL.md。
- **Go 机制**:共享包必须从 `internal/` 挪到可公开 import 的路径(`internal/` 包跨 module 引不到);opskit-core 为独立 module,打 semver tag;消费方 `require` 它,本地联调用 `replace` 指向本地目录。
- 分叉点规范形状:风险常量用 sentinel 的 `R0/R1/R2/R3`;AllowFlag 值不带 `--` 前缀;R2 授权采 sentinel 更严形(除 ticket 外还要 `--yes`/确认);RBAC 进 core;`ValidateBackupPolicy` 进 core。
- **护栏**:改 nacos 的 AllowFlag 值格式 / R2 行为可能破坏其已发布契约和 golden,涉破坏性变更须走版本号 + 迁移流程,不为 dbgov 需求悄悄掀掉稳定的 nacos 契约。
- 次序:抽 core → 先迁 sentinel(验证接口)→ 再迁 nacos → 最后建 dbgov。**绝不再造第三份复制粘贴。** 赶时间的底线:定义好 core、dbgov 直接 import 新建,另两个 fast-follow 迁移。
