# Magpie 多 Agent 开发协同扩展：源码分析与开发计划

> 状态：设计草案（仅规划，尚未实现功能）
> 日期：2026-11-06
> Fork：https://github.com/SicL/magpie
> 上游：https://github.com/yetone/magpie
> 基线：baf70992f6b2860ba1559da71098dfdf18bdc360
> 开发环境：GTR Windows，D:\magpie
> 目标工具：WorkBuddy、DeepSeek Harness、ZCode（后续可扩展）

## 1. 目标与边界

在保留 Magpie 模型网关、Agents、Library、Usage、Sessions 与配置能力的前提下，新增“Projects / Tasks”协同管理。让多个 AI 编程工具在同一个 Git 项目中安全地并行、交接与审查，最终到 MR 验收闭环。

第一版不重写 Git，不创建通用 Agent 协议，不侵入第三方应用的私有数据库，也不假设现有模型配置适配等于可远程启动 Agent。优先用现有工具的 CLI/SDK/ACP（需按当前安装版本验证）；不能自动执行的工具允许人工启动、自动记录与交接。

## 2. 已核实的上游源码结构

| 子系统 | 已存在的实现 | 本项目复用方式 |
| --- | --- | --- |
| Agent 配置适配 | internal/agent/agent.go；workbuddy.go、dsh.go、zcode.go | 识别工具、读取模型配置、展示连接状态；执行适配器另行设计 |
| 会话管理 | internal/sessions/manage.go、sessions.go；gui/sessions_manage.go | 关联现有 Agent 会话、目录、消息与 ResumeCommand；不重新解析全部会话格式 |
| 内置插件系统 | internal/pluginrt/pluginrt.go；plugins/all/all.go；plugins/sessions/sessions.go | 新建内置可开关的协同插件，并沿用生命周期与 nav.page 机制 |
| Web / 桌面页面 | internal/gui/api.go、assets/kernel.js、assets/plugins/sessions/client.js | 增加页面与 JSON API；遵循现有页面风格、i18n 和 UI 测试 |
| SQLite | internal/sqlitedb/sqlitedb.go；go.mod 的 modernc.org/sqlite | 复用 SQLite 驱动入口与安全环境处理，独立任务数据库 |
| 本地命令 / 进程 | internal/proc/ | 统一进程执行、限制工作目录与生命周期；不裸用 exec.Command |
| 规范与验收 | AGENTS.md、CLAUDE.md、LESSONS.md、docs/code-standards.md、docs/subsystems/ | 先遵守现有规范，再增加新子系统设计文档 |

重点：Magpie 的 Sessions 页已经能读取 WorkBuddy、DeepSeek Harness 的会话，并把 ZCode 的会话列出，但并不等于三者均支持自动接力、自动非交互运行；需要单独验证执行能力。

## 3. 推荐架构

采用“内置插件 UI + Go 协同服务 + Git”方案，而不是把调度逻辑放在供应商模型网关内。

    Magpie 主窗体
      ├─ Agents / Library / Sessions / Usage（保持原功能）
      └─ Projects & Tasks（新增内置插件页面）
           │ JSON API（现有 Handler + 新路由）
           ▼
    internal/orchestration（新增；无 GUI 依赖）
      ├─ Project Registry / Settings
      ├─ Task Graph + Lease（单任务写入者）
      ├─ Change / Checkpoint Store（SQLite）
      ├─ Worktree Manager（Git CLI，路径受限）
      ├─ Execution Adapter Registry（能力检测）
      ├─ Run Supervisor（进程、日志、取消）
      └─ Review & CI Coordinator（Git/MR 交付）
           │
           ├─ WorkBuddy adapter
           ├─ DeepSeek Harness adapter
           └─ ZCode adapter

建议新增位置：
- internal/orchestration/：模型、状态机、持久化、Git、进程、适配接口
- plugins/orchestration/orchestration.go：Manifest，nav.page=orchestration；注册在 plugins/all/all.go
- internal/gui/orchestration.go：后端 API 路由与校验
- internal/gui/assets/plugins/orchestration/：client.js、i18n.js、orchestration.css
- internal/cli/orchestration*.go：随后增加等价的 CLI 命令
- docs/subsystems/orchestration.md：实现后存放真实运行契约（非愿景）

注意：上游现有 built-in plugin 使用 Go Manifest 和 UI Slot；社区 npm Provider/Middleware 插件不负责本地任意进程调度。不能把社区插件视为执行宿主。

## 4. 数据对象与状态

Project：ID、Git 根目录、基线分支、允许的 Worktree 根、规则文件、可用 Agent 角色、权限、测试命令、MR 目标。

Task：ID、ProjectID、标题、目标、验收条件、依赖任务、状态、负责 Agent、创建/更新时间、重试次数。

Change：ID、TaskID、Repo、BaseCommit、HeadCommit、文件清单、测试证据、审查状态、PR/MR 链接。Change 不等同于 Agent 专属分支。

Run：ID、TaskID、AdapterID、WorktreePath、SessionID（若可获得）、启动参数摘要、进程信息、日志位置、心跳与退出信息。密钥不写入日志。

Checkpoint：TaskID、Git HEAD、未提交变更摘要/备份引用、完成与剩余步骤、测试结论、handoff 文本、时间。

状态机：
- pending → ready（前置依赖完成）
- ready → claimed → running（原子认领 / 租约）
- running → awaiting_review → accepted
- running → blocked / failed / paused
- 失联时标为 uncertain，先检查进程与 Git 状态再恢复；不能看到心跳过期就并发启动第二写入者
- failed → ready 只能经过明确的重试规则；accepted 不得由 Agent 口头自报

并发规则：项目级多任务并发；同一个任务同一时刻只有一个写入 Owner。通过 Lease 和 Git 文件锁实现，SQLite 事务防止双重认领。所有写操作必须检查任务版本与所属分支。

## 5. Git 与工作区规范

- 原仓库与各 Agent Worktree 分离；Fork 的 main 只用于同步上游，不直接开发。
- 每个任务分支建议 agent/<task-id>/<role>；规范化文件名与路径，禁止绝对路径逃逸、符号链接逃逸。
- Worktree 必须从被确认的基线 Commit 创建。必须识别 Git 仓库根目录及 nested Git/submodule，禁止盲目在非仓库下执行。
- 开始前检查未提交修改、分支是否被占用、目标目录是否已存在。
- 不使用 git reset --hard、git clean -fd 或强制删除现有工作区作为自动恢复手段。
- 交接优先使用可验证的 Git Commit；未提交内容只允许建立受保护的补丁/快照，不能自动覆盖。
- 变更冲突创建独立 Review/Fix 任务，禁止 Agent 直接合并他人未确认的改动。
- 集成由专用分支和独立 CI 完成；测试通过后创建 MR，不自动推送生产分支。

## 6. Agent 适配器合同

统一接口只定义已能可靠执行的能力：
- Detect：是否安装及版本
- Inspect：配置、会话能力、当前进程（只读）
- Prepare：检查工作区与任务输入
- Start：受控启动（可选，需有可验证 CLI/SDK/ACP）
- Observe：运行状态、退出码、输出、心跳（可选）
- Stop：仅结束本平台自己启动且可验证归属的任务进程（可选）
- Handoff：输出标准交接记录和提交标识
- Resume：优先原生会话恢复；不支持时用结构化检查点重新启动

按工具逐个验收：
1. DeepSeek Harness：检查实际 CLI/ACP 和 Team 与工作区范围；避免把内部 Agent Teams 误当跨进程管理。
2. ZCode：检查当前版本有无非交互 CLI/SDK；只有模型配置能力时先支持“人工启动+自动读取状态”。
3. WorkBuddy：检查 Worktree Task 与 CLI 能力；优先复用其原生 Worktree；验证受控启动前不自动关闭/重启应用。

执行适配器与 Magpie 模型配置适配器分离，不复用其读写私有配置的权限作为程序执行权限。未来新增 Agent 只扩展 Adapter。

## 7. UI 与配置

页面一：项目列表／创建项目：Git 根、基线、Worktree 根、规则、允许的 Agent、只读校验。

页面二：任务板：任务依赖、Owner、状态、阻塞原因、验收标准、可操作的执行/暂停/恢复按钮；不做不可解释的“完成百分比”。

页面三：运行详情：执行 Agent、工作区、会话、Git Head、步骤日志、心跳、退出码、费用（可从 Usage 关联），以及失败原因。

页面四：Change / Review：代码差异、测试证据、交接文件、MR 目标与审查结论。

配置：全局默认 + 项目覆盖；AGENTS.md / CLAUDE.md 必须从真实仓库读取，不用全局模板覆盖用户配置。首次发现变更时给出 Diff 和选择。

界面遵循 Magpie 原有组件、菜单、页面 Slot 与翻译规则；至少覆盖简体中文、英文、繁体中文、日文、德文，并验证窄屏。

## 8. 安全边界

- 仅允许授权项目根与 Worktree 根下的读写；验证真实路径、符号链接、Git 根与分支。
- 对本地目录中的现有工作用户文件执行无损策略；禁止随意删除、重置、覆盖。
- 不自动访问生产数据库；TMS 只允许既有 API/测试环境访问，绝不通过 Agent 启动器绕过此约束。
- 不允许结束无关进程、服务、Docker 容器；只有本平台启动且能验证身份的子进程可以停止。
- 后台命令采用参数数组，禁止拼接可执行 shell 注入；记录命令摘要，密钥从安全配置注入且过滤日志。
- Web 访问默认仅本机可写。远程 Web UI 不应继承“已登录即能执行本地命令”的权限；必须另设认证/授权与开关，MVP 先保持只读。
- 任务日志、SQLite 数据库、Patch 文件避免保存 API Key、个人隐私、账号令牌。遵循现有 Magpie 隐私机制。
- 实际修改生产 Git 分支、提交 MR、合并、删除任务工作区等动作各有独立审批政策，默认不自动合并。

## 9. 分阶段开发计划与验收

### P0：环境/能力基线
输出：GTR 上三个 Agent 的安装路径、版本、CLI/SDK/ACP/会话能力矩阵，当前 Magpie 的 Go/Node/build 状态，Windows GUI 与 CLI 构建验证；确认 TMS Git 仓库结构与只读规范。
验收：每个 Agent 的可用/不可用能力有实际命令或 UI 证据；不能执行的操作标注手动模式。
当前发现：本机可使用 Git、GitHub CLI 与 Node；Go 尚未在 PATH 中发现，因此 Go 构建/测试尚未运行。

### P1：Projects & Tasks 基础页面
输出：内置插件页、项目列表及任务 CRUD、独立 SQLite 库、配置校验与数据库迁移。
验收：Windows UI 正常显示，重启后状态持久化；新插件关闭不影响原有 Magpie 页面与网关；非法路径、重复项目、并发更新有测试。

### P2：Git Worktree 与 Change 管理
输出：工作区创建/列举/安全回收、Git Commit 与 Change 关联、任务依赖图、结构化 Checkpoint、原子 Lease。
验收：两个 Agent 任务的代码互不覆盖；断电/崩溃后能恢复真实任务状态；不能重复分配同一写入任务；绝无自动删除原有工作数据。

### P3：三个 Agent 接入
输出：Adapter Registry、Detect/Inspect/Start/Observe/Stop/Resume 能力矩阵，至少一个真实受控执行 Adapter，其他工具按验证结果实现自动或人工模式。
验收：启动命令与工作目录可追踪；只停止自己启动的任务；无 CLI 的工具可正常人工交接；不宣称未验证的自动能力。

### P4：跨 Agent 接力和审查
输出：会话与 Task/Change 关联、标准交接记录、差异比较、跨 Agent Review 与阻塞回退。
验收：Agent A 完成部分任务，Agent B 能通过 Commit + Checkpoint 继续而不重复；修改冲突可定位、可复核；Review 不自动覆盖代码。

### P5：CI 与 MR 闭环
输出：构建/测试门禁、测试结果保存、GitHub/GitLab MR 适配、自动生成变更说明，失败回流修复任务。
验收：任务需要有真实测试证据才能 accepted；未满足门禁不能合并；测试失败有可重跑记录；上游同步不影响已分离的开发分支。

### P6：稳定性与维护
输出：多机/跨平台适配验证、配置升级兼容、异常恢复审计、上游同步演练、性能基准。
验收：Windows 主环境运行稳定；默认禁用的协同插件不改变现有模型配置/网络路由/会话行为；升级回滚与迁移有测试。

## 10. 核心 MVP 演示流程

1. 创建 Project（例如独立测试仓库），设定基线与规则。
2. 建立规则分析、后端、前端、Review 四项带依赖的任务。
3. 为两个写入 Agent 建立互不干扰的 Worktree。
4. 分配任务，通过原生 CLI 或人工启动；记录 Run/Session/Change。
5. 模拟中断，检查原进程、保存 Checkpoint，安全接力给另一 Agent。
6. 执行独立 Review 与模拟 CI；只有通过后生成 MR 草稿。
7. 重启 Magpie，确认任务、变更与审查证据仍可恢复。

## 11. 测试计划与发布门槛

遵循 docs/code-standards.md 与 docs/subsystems/README.md：
- 使用临时 HOME，不让测试接触用户真实 Magpie/Agent 配置；固定 Go 缓存避免大规模重复下载。
- 先写回归测试并确认旧行为失败，再确认新行为通过；后台/并发改动使用 race 检测。
- Go：gofmt、go vet、Linux/Windows 交叉检查、go build、go test -tags nogui ./...；增量包测试不是整个项目通过的替代。
- GUI：Chromium + WebKit 的 Playwright 测试；包括多语言、窗口尺寸、插件开关、权限拒绝。
- Windows 实机：路径、长路径、空格、进程树终止、Git 锁、账号权限、第三方 Agent 实际启动。
- Git 集成测试使用临时本地仓库，不接触真实项目或远端仓库；CI 测试使用模拟服务。
- 实现后同步 docs/subsystems/orchestration.md；MR 说明必须列出运行与未运行的检查。
- 首个功能版本应为 opt-in，支持只读观察和紧急停用，绝不影响 Magpie 核心服务。

## 12. 风险与先行验证

| 风险 | 对策 |
| --- | --- |
| 不同 Agent 没有稳定自动化入口 | P0 实机验证；能力为可选；人工执行兜底 |
| Agent 自带 Worktree 与平台管理冲突 | 可选择 native/external Worktree 模式，绝不同时双重创建 |
| 会话格式不兼容 | 复用 internal/sessions；以 Git + Checkpoint 为最终交接载体 |
| 跨进程竞争、重复恢复 | SQLite 事务、Lease、进程检查、Git 锁与幂等动作 |
| 上游频繁更新 | 新增独立 package/plugin，控制对核心文件的修改面，定期合并 upstream/main |
| 用户真实配置或生产系统被影响 | 只读默认、隔离测试、最小权限、绝不清理未知目录/进程 |
| Windows Go 构建环境缺失 | P0 先安装/验证受控工具链，未通过前不宣称可构建 |

## 13. 本次已完成与未完成

已完成：在 GitHub 创建 SicL/magpie Fork；本机克隆 D:\magpie；设定 origin/upstream；阅读根 AGENTS.md、CLAUDE.md、代码规范、子系统设计文档；定位三个 Agent 配置适配器、Sessions、内置插件、GUI API、SQLite 与进程工具的真实代码入口；形成此计划。

尚未完成：Go 编译与现有测试基线（本机 PATH 尚未发现 Go）；三个工具的真实 CLI/ACP 自动化能力实测；新协同插件代码；UI 测试；MR / PR。规划分支不代表功能已开发。

下一步严格从 P0 开始，不在缺少可执行能力证据的情况下为 WorkBuddy/ZCode/DSH 伪造统一启动器。
