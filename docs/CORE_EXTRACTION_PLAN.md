# opskit-core 抽取与迁移施工单

> 目标:把 nacos-cli / sentinel-cli 中重复的治理外壳抽成独立 Go module `opskit-core`,对齐已分叉的规则,再让 dbgov 直接复用。
> 关联:见 `DESIGN.md`(dbgov 设计)。本施工单是 dbgov 编码前的前置工程。
> 状态:已执行完毕(行为收敛至 5c)。

---

## 1. 原则

- **只抽真正通用的治理外壳,领域代码留各自项目**(避免共享耦合)。
- **增量抽,不大爆炸**:逐包迁移,每步保持各工具测试绿。
- **以 sentinel 为代码捐献基底**(它最成熟:有 RBAC、OTel、vault、追加式审计 + verify、pkg 对外 API)。
- **顺序降风险**:低契约风险的 sentinel 先迁;差异最大的 dbgov 早参与验证 core API;契约最敏感的 nacos 最后迁。
- **破坏性变更走版本号**:涉及用户可见行为变化的(主要是 R1/R2 的 `--yes` 语义)在 nacos 走 minor/major + 迁移说明。

---

## 2. 顺序总览

```
Phase 0  建 opskit-core module 骨架
Phase 1  抽无争议包(apperrors / lockfile / printer / credstore)  → 迁 sentinel → 测试绿
Phase 2  抽 ctx 框架 / audit                                      → 迁 sentinel → 测试绿
Phase 3  抽 safety(对齐分叉)+ telemetry + 输出信封             → 迁 sentinel → 测试绿
Phase 4  在 core 上搭 dbgov 薄竖切(ctx + schema diff)           → 验证/回填 core API
Phase 5  迁 nacos 到 core(含版本号 + 契约说明)                  → golden/测试绿
Phase 6  core 收口 v1.0.0;dbgov 正式开发
```

核心理念:**sentinel 先迁(低风险)→ dbgov 早验证(最不一样的消费者)→ nacos 最后迁(最敏感)。**

---

## 3. 抽取范围

### 3.1 进 core(通用治理外壳)

| 包 | 来源基底 | 备注 |
|---|---|---|
| `apperrors` | 两者一致 | 错误码 + 信封 |
| `lockfile`(+ pid_posix/windows) | 两者一致 | |
| `printer` | sentinel | 输出渲染 |
| `credstore`(keychain/encrypted/plain/vault/reference) | **并集** | sentinel 有 vault;取两者并集 |
| `ctx`(框架部分) | sentinel | **仅基类**:name/server/env/protected/ticketPattern/creds-ref/roles/otel + 存取;领域字段各 CLI 扩展(见 §3.3) |
| `audit`(audit/query/verify) | sentinel | sentinel 更全(query + verify:JSON/schema/时序校验 + 坏行隔离,非哈希链;可选 age 加密) |
| `safety`(risk/ticket/RBAC/authorize) | sentinel + 对齐 | 分叉点见 §5 |
| `telemetry`(OTel) | sentinel | 默认脱敏 |
| `envelope`(apiVersion/kind 输出信封) | nacos | nacos 的契约更成形 |
| `doctor`(检查 harness + 输出) | sentinel | 仅框架,领域检查各 CLI 注入 |
| `capabilities`(信封框架) | 两者 | 框架共享,feature 列表领域注入 |

### 3.2 留各 CLI(领域代码)

- 具体 backend 实现(nacos config/service API、sentinel rule、dbgov SQL)
- 领域命令(`cmd/`)、领域校验、领域 doctor 检查、领域 capabilities 列表、`SKILL.md`

### 3.3 两个"半共享"的难点,单独处理

- **ctx**:core 提供**上下文基类 + 存储/加载/凭据引用 + RBAC 角色 + OTel 配置**;领域字段(nacos: `namespace`;sentinel: `backend`/`apolloAppId`;dbgov: `engine`/连接拓扑)由各 CLI 通过组合(嵌入 core 的 `ctx.Base`)扩展。**不要**把领域字段塞进 core。
- **backend**:sentinel 的 `Backend` 接口是规则专用(`GetRules/PutRules`),**不通用**。core **只**提供最小约定(`Ping() / Describe()`)+ 连接/凭据装配;领域方法(GetRules vs config API vs SQL exec)各 CLI 自定义接口。**backend 主体留领域,不进 core。**
- **backup**:core 提供通用"备份存储/索引"框架;"备份什么"领域决定。dbgov 注意 DB 上备份是有界/有损的(见 DESIGN.md Q3),不照搬"存旧值"模型。

---

## 4. Go 机制

- 新建独立 module:`github.com/JiangHe12/opskit-core`(独立仓库),`go.mod` 声明该 module path。
- **共享包必须放在可公开 import 的路径**(顶层包或 `pkg/`),**不能在 `internal/` 下**——`internal/` 包跨 module 引不到。这是抽取第一步要做的"挪窝"。
- 版本:core 打 semver tag(`v0.x.y` 开发期,稳定后 `v1.0.0`)。
- 消费方:`go.mod` 加 `require github.com/JiangHe12/opskit-core vX`,import 路径改为 `.../opskit-core/<pkg>`。
- **本地联调**:消费方 `go.mod` 加
  ```
  replace github.com/JiangHe12/opskit-core => ../opskit-core
  ```
  调好后去掉 replace,改用正式 tag。

---

## 5. 分叉点对齐决策(safety,Phase 3 前必须先定)

| 分叉点 | 现状 | 规范形状 | 是否用户可见 |
|---|---|---|---|
| 风险常量名 | sentinel `R0..` / nacos `RiskR0..` | `R0/R1/R2/R3` | 否(内部),自由对齐 |
| AllowFlag 值格式 | sentinel 无 `--` / nacos 带 `--` | **不带 `--`**(`--` 是 CLI 展示层的事) | 否(内部),自由对齐 |
| R1 非交互 | nacos 要 `--yes` / sentinel 放行 | **要 `--yes`**(取更严) | **是**(sentinel 行为收紧) |
| R2 授权 | sentinel ticket+`--yes` / nacos 仅 ticket | **ticket + `--yes`**(取更严) | **是**(nacos 行为收紧) |
| RBAC | sentinel 有 / nacos 无 | 进 core,**opt-in**(仅配了角色时生效) | 否(加法) |
| `ValidateBackupPolicy` | nacos 有 / sentinel 无 | 进 core | 否(加法) |

**护栏**:用户可见的两处(R1/R2 的 `--yes`)都是**往更安全方向收紧**,方向正确;但要更新两个工具的 golden/测试,并在 nacos(契约更严)的 CHANGELOG/版本说明里写清。

---

## 6. 每包迁移的标准动作(可复制的配方)

对每个进 core 的包,重复:

1. **挪窝**:从捐献者(sentinel)的 `internal/<pkg>` 复制到 `opskit-core/<pkg>`(公开路径)。
2. **去领域化**(关键,易漏):清掉 sentinel 专属内容,参数化注入:
   - prompt 文案(如 `"Proceed with rule write?"` → 通用 `"Proceed with write?"`,或由调用方传入)
   - 环境变量前缀(`SENTINEL_OPERATOR`/`SENTINEL_PASSWORD` → core 接受可配前缀,各 CLI 注入 `NACOS_`/`DBGOV_`)
   - 审计事件词汇(`rule.import` 等 → 由调用方传入事件类型)
   - 任何写死的 dataId/group/资源名
3. **打 tag**:core 发一个新的 `v0.x`。
4. **替换**:sentinel 删除本地 `internal/<pkg>`,import 改为 core;加 `replace` 本地联调。
5. **验证**:`go build ./... && go test ./...`(sentinel)全绿;golden 无意外 delta。
6. nacos 在 Phase 5 重复 4–5。

---

## 7. 分阶段细节

### Phase 0 — core 骨架
- 建仓库 + `go.mod`(module path)+ 基础 CI(`go vet`/`golangci-lint`/`go test`)。
- 验证:空 module 能 build。

### Phase 1 — 无争议包 + 迁 sentinel
- 抽 `apperrors`、`lockfile`、`printer`、`credstore`(并集,含 vault)。
- 按 §6 配方迁 sentinel。
- 验证:sentinel 全测试绿。

### Phase 2 — ctx 框架 / audit
- 抽 `ctx` 基类(§3.3)+ `audit`(含 query/verify)。
- sentinel 的 `sentinelctx` 改为"嵌入 core `ctx.Base` + 领域字段"。
- 验证:sentinel 全测试绿;审计链 `audit verify --strict` 通过。

### Phase 3 — safety + telemetry + envelope
- **先落 §5 对齐决策**,再抽 `safety`(对齐后的)、`telemetry`、`envelope`。
- 更新 sentinel 因 R1/R2 收紧而变化的测试/golden。
- 验证:sentinel 全测试绿;授权用例(R0–R3)行为符合规范形状。

### Phase 4 — dbgov 薄竖切验证 core
- 在 core 上新建 dbgov-cli,只做最小竖切:`ctx set/use` + 一条 `schema diff`(只读)。
- 目的:用差异最大的消费者压测 core API,暴露缺口(如:按 backend 声明能力、EXPLAIN 影响行数进风险输入、破坏性 diff 标红的钩子)。
- **把缺口回填进 core**(core 再发一个 `v0.x`)。
- 验证:dbgov 薄切能 build + 跑通 `schema diff` dry-run。

### Phase 5 — 迁 nacos(最后,最敏感)
- 按 §6 配方迁 nacos 全部进-core 的包。
- 处理用户可见行为收紧(R1/R2 `--yes`):更新 golden、写 CHANGELOG/迁移说明、按其版本规范走版本号。
- 验证:nacos 全测试 + golden 绿;契约文档更新。

### Phase 6 — 收口
- core 稳定后发 `v1.0.0`,三个工具锁定该版本(去掉 `replace`)。
- dbgov 进入 DESIGN.md 定义的正式开发。

---

## 8. 验证策略(贯穿)

- 每个 Phase 结束:相关工具 `go build ./... && go test ./...` 必须全绿;有 golden 的(nacos)`golden` 无意外 delta。
- core 自带的治理逻辑(safety/audit)迁入后,把捐献者原有的对应测试一并迁入 core,形成 core 的测试套件(集中、唯一)。
- 行为对齐的回归点:R0–R3 授权矩阵、ticket/allow 校验、审计链完整性、凭据后端读写。

---

## 9. 风险与回滚

| 风险 | 缓解 |
|---|---|
| 迁移破坏在跑的工具 | 增量 + 每步测试绿;出问题就停在上一个绿色 Phase |
| core API 被两个老工具拍死、dbgov 才发现缺口 | Phase 4 提前用 dbgov 薄切验证,再回填 |
| nacos 契约破坏 | 用户可见变更仅 R1/R2 `--yes`(往更安全收紧);走版本号 + 迁移说明 |
| 去领域化不彻底(残留 sentinel 字样) | §6 step 2 清单化检查;core CI 加关键字扫描(禁止出现领域专属串) |
| 共享耦合(core 变动牵动三方) | 严守"只抽通用外壳",backend/领域字段不进 core |

---

## 10. 一句话

**sentinel 先迁验证抽取 → dbgov 薄切早验证 API → nacos 最后迁走版本号**,每包按"挪出 internal/ + 去领域化 + 替换 + 测试绿"的配方增量推进,最终三工具共享一份经过审计、规则对齐的治理脊椎。

---

## 11. 实际执行(as-built)

本节记录执行结果与原计划的关键偏离;上文保留为当时的施工计划,不按事后结果逐行改写历史。

- **audit**:实际采用"共享引擎 + 各工具自有 Event 结构"。core 提供 append/lock/rotate/age/quarantine/verify/raw query 引擎,并通过配置注入 timestamp/type/operator 等 schema 键名;sentinel、nacos 保留各自 Event/事件常量/查询展示壳。原因是 sentinel 与 nacos 的落盘字段、键名、嵌套结构差异较大,用单一超集 struct 会脆弱且容易破坏格式。
- **输出信封(envelope)**:实际并入 `printer` 包做可选、参数化能力,不是独立包,也不是 Phase 3 一次性落地。现有裸 JSON 输出保持默认,需要信封的工具显式注入 apiVersion/kind 等样式。
- **ctx**:实际收敛为共享 Store 持久化引擎 + 可选 Base。sentinel/dbgov 继续嵌入 `ctx.Base`;nacos 因配置 schema 不同,使用 `NewStoreWithoutBase` 接入 Store,保留自己的 Context 类型和 yaml 字段。
- **Phase 5 实际拆分**:5b-1(audit 引擎支持外来 Event) → 5b-2a~2f(apperrors/credstore/telemetry/printer/ctx/audit 逐包迁 nacos) → 5b-3(兼容债清理:删除 nacos 重复 encrypted-file、keychain 统一 bare contextName、nacos ctx `otel*` 收敛为 `otlp*`) → 5c(nacos 迁 core safety,R1/R2/R3 收紧为均需 `--yes` 或交互确认)。
- **贯穿原则**:共享引擎在 core,各工具领域类型/命令壳/文案/字段名通过本工具保留或注入;分叉行为取更正确者为家族标准,例如 apperrors 退出码采用 nacos 精细映射、vault 错误分类采用精细映射、safety 采用更严格的 R1/R2/R3 授权规则。
- **家族目标**:opskit-core 已被 sentinel-cli、nacos-cli、dbgov 复用;未来 es-cli / mq-cli 等治理工具按同一契约接入,避免再复制治理外壳。
