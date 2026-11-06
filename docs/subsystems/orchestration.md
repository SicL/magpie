# Projects & Tasks orchestration subsystem

> Implemented scope: P1/P2 registry and Worktree, P3 DSH execution, P4 checkpoints, continuation and audited manual recovery.
> Initial release: opt-in built-in plugin. See [design proposal](../proposals/multi-agent-orchestration-plan.md).

## Responsibilities and boundaries

The Projects page organizes work across coding agents without changing Magpie's model gateway, provider accounts, agent config, Library, Usage, or native sessions.

| Component | Responsibility | Source |
| --- | --- | --- |
| Project and task store | SQLite database, Git validation, task versioning | [store.go](../../internal/orchestration/store.go) |
| Git Worktree operations | Explicit creation and read-only listing, no force/reset/remove | [worktree.go](../../internal/orchestration/worktree.go) |
| HTTP API | JSON endpoints with bounded request size and desktop-only mutations | [orchestration.go](../../internal/gui/orchestration.go) |
| Built-in plugin | Optional feature, page registration and assets | [plugin](../../plugins/orchestration/orchestration.go) |
| Window UI | Project/task forms and manual state actions | [client.js](../../internal/gui/assets/plugins/orchestration/client.js) |

## Source of truth

- Task state is in the magpie config directory under orchestration/tasks.db.
  It is created lazily on the first request, not at startup.
- Git remains the authority for repository root and branch names.
  Registering a project never modifies its repository.
- Model routing and agent configuration remain owned by internal/agent.
- The agent field on a task is a display assignment, **not** an executable adapter or proof that an external process is running.

## HTTP API

- GET /api/orchestration/projects: list projects.
- POST /api/orchestration/projects: register an existing local Git root and branch; 201 on success.
- POST /api/orchestration/projects/{id}/remove: remove metadata only if no tasks exist.
- GET /api/orchestration/tasks?project={id}: list tasks.
- POST /api/orchestration/tasks: create a pending task.
- POST /api/orchestration/tasks/{id}/status: update by current revision. A stale revision is 409; an invalid transition is 400.
- POST /api/orchestration/tasks/{id}/remove: delete metadata for pending, blocked, failed, or done tasks only.
- GET /api/orchestration/worktrees?project={id}: list only project-scoped agent/task worktrees.
- POST /api/orchestration/tasks/{id}/worktree: explicitly create an isolated Git checkout; 201 on success. A duplicate target returns 409.

Mutations are disabled in browser/Web mode in P1, even on loopback; the desktop app is required. The existing Magpie Web page guard protects read-only APIs. Git commands use separate argv arguments through internal/proc. No shell interpolation is permitted.

## State transitions

- pending → ready or blocked
- ready → running or blocked
- running → review, blocked, or failed
- blocked/failed → ready
- review → done or ready
- done is terminal

These are **manual task records**, not actual Agent process states. Updates use compare-and-set by integer revision.

## Compatibility and errors

- A non-Git directory, a path below the Git root, a missing local branch, and a worktree root inside the repository are rejected.
- Duplicate repositories are rejected; projects with tasks and active tasks cannot be deleted. Tasks that own an existing Git Worktree cannot be removed as metadata.
- A SQLite read error is not an empty result.
- Toggling this plugin off does not delete project data or modify existing Agent configurations or sessions.
- The database is independent of existing Magpie settings and gateway session stores.

## Verification

- Go package tests: go test -tags nogui ./internal/orchestration ./internal/gui
- Full baseline: go vet ./..., go test -tags nogui ./..., and supported platform builds.
- JS syntax: node --check on orchestration/client.js and orchestration/i18n.js.
- Chromium and WebKit Playwright tests, including translated UI and reduced width.
- Functional tests use a temporary local Git repository, never a TMS or live user repository.
- Follow the root AGENTS.md and docs/code-standards.md: no tests in the user's real Agent home, and no network access from tests.

Creating a Worktree is an explicit user action: the tool validates the real Git root, checks symlink resolution, chooses a task-specific branch and directory, and uses Git without --force. It never removes or resets the checkout, including after a command failure. Worktree listings come from Git itself. Concurrent multi-process execution leases, automated Agent startup, MR approval and cleanup remain future milestones; this registry is not yet a scheduler.

## P3 DeepSeek Harness headless execution

The first executable adapter is deliberately limited to DeepSeek Harness.
Only a desktop user action can start it, and only for a task whose current
status is ready, whose agent is DeepSeek Harness, and whose task-specific
Git Worktree exists. WorkBuddy and ZCode remain manually coordinated.

### Run lifecycle and ownership

- The existing user-visible task status remains the workflow state.
- Each process has a separate durable Run record in agent_runs with an ID,
  project task, adapter, process ID, Git Worktree, timestamps, exit code,
  failure category and native DSH session ID.
- Runs transition through starting, running and stopping into completed,
  failed or cancelled. After an unexpected Magpie supervisor restart,
  unfinished runs are marked interrupted **and stay unclaimable** until
  their old process and working tree can be inspected. Do not silently
  relaunch a task just because a process ID is missing.
- The latest process outcome advances an unmodified running workflow task
  to review (exit 0), failed (non-zero exit), or blocked (explicit stop).
  This does not imply that tests or the user acceptance criteria passed.
- One active or interrupted run per task is enforced by a SQLite partial
  unique index. Up to three runs can be active in one Magpie supervisor.
- RunService uses the existing internal/proc.StartTree command grouping.
  Stop only targets a process tree started and owned by this exact
  supervisor, never an arbitrary PID or a different running app.
- The actual CLI command is DeepSeek Harness' headless profile with
  structured JSON events read from stdout. Task instructions go through
  stdin; the prompt is not duplicated into the log. A native session
  event with sessionId is captured and stored for later handoff support.
  P3 records the ID but **does not yet offer automatic session resume**.

### HTTP API

- GET /api/orchestration/agents/dsh: CLI availability and version.
- GET /api/orchestration/runs?project={id}: run metadata.
- GET /api/orchestration/runs/{id}: one run's metadata.
- POST /api/orchestration/tasks/{id}/run: explicit launch, with task revision
  in the JSON body.
- POST /api/orchestration/runs/{id}/stop: stop only current-supervisor-owned
  process tree.
- GET /api/orchestration/runs/{id}/log: up to 32 KiB of local log tail.

Starting and stopping runs require JSON, reject null/cross-site origins and
are unavailable in browser/Web mode. Log content is also unavailable in
browser mode. Browser mode may show metadata, not private logs.

### Security and privacy

**Git Worktree isolation is not an OS sandbox.** A DSH agent can execute
commands with the permissions of the logged-in local user. The desktop UI
warns about this; tasks are launched only after an explicit user action.
Do not test execution against TMS production, live agent settings, or
unrelated project folders.

Run logs are stored only in the local Magpie configuration directory under
orchestration/run-logs. They use restricted creation permissions, a 2 MiB
size limit, and best-effort token redaction. Because arbitrary tool output
may contain additional secrets, **log sanitization is not a guarantee**;
treat log files as sensitive. Native DSH session content is still owned
by DSH, not copied into Magpie's task database.

No process is automatically launched from a user-created task. No task
is automatically merged to Git main or submitted as an MR. Automated
credential handling, process crash reconciliation and real Linux/macOS
tree supervision still require separate review and verification.

## P4 native DSH continuation and cross-Agent handoff

P4 builds on P3. Registry, Worktree, tasks, Runs and checkpoints remain
separate from Magpie's model gateway and Agents' own configuration stores.

### Checkpoints

- An operator creates a checkpoint through POST /api/orchestration/tasks/{id}/checkpoints
  with current task revision, 1-4000 character summary, and test status
  not_run, passed or failed. Passed is a **self-reported assertion**, not
  verification by Magpie or CI.
- The server records Git HEAD and whether the task-specific Worktree has
  uncommitted changes. It does not modify tracked source or store filenames.
- Checkpoints live in the agent_checkpoints SQLite table, independent of
  Run logs. An optional Run reference must belong to the same task and
  Worktree. GET endpoints retrieve checkpoint and recovery histories.
- Desktop Copy task handoff includes task description, Worktree/branch and
  the latest checkpoint. The user deliberately pastes this into WorkBuddy,
  ZCode or another Agent. Magpie does not upload it automatically.

### DSH native continuation

- The source Run must be the latest terminal completed/failed/cancelled DSH
  Run for the same task and exact Worktree, with a valid stored sessionId.
- After manually returning the workflow to ready, POST
  /api/orchestration/tasks/{id}/resume accepts only a local sourceRunId.
  It does not accept an arbitrary native session ID or shell arguments.
- The adapter sends --session-id plus the recorded ID to DSH, with task
  instructions and latest checkpoint via stdin. It creates a new Run with
  resumeOf pointing to the source record; prior Runs stay unchanged.
- Interrupted, reconciled, other-task and stale/older sessions cannot be
  resumed. No automatic retry, merge or acceptance is performed.

### Interrupted-run reconciliation

- Supervisor restart leaves uncertain Runs interrupted and locked.
- POST /api/orchestration/runs/{id}/reconcile requires desktop mode, a
  current revision, explicit human attestation and a 12-500 character audit
  reason. It never terminates a process.
- Platform process APIs check the old PID. Active, unknown or inaccessible
  processes fail closed; recycled PIDs are treated as potentially live.
- With an absent process and the operator's explicit confirmation that
  its children stopped, the original Worktree is inspected. agent_recoveries
  stores Git HEAD, dirty flag, old PID, note and timestamp.
- The old Run becomes reconciled and the task becomes blocked, **not ready**.
  A separate manual decision must make the task ready before another Agent
  can start. A reconciled DSH session cannot be resumed directly.
- Parent PID absence does not prove every grandchild/external command
  stopped. The operator must verify that. Never kill by global PID pattern.

### Provider boundaries

On the checked GTR installation, WorkBuddy 5.9.3 and ZCode 3.17.2 are
desktop apps without a validated headless CLI on PATH. A separate
CodeBuddy Code CLI and ZCode CLI provide documented noninteractive
interfaces, but integration/authentication with the installed desktop
app is not verified. For now, WorkBuddy/ZCode use manual handoff only.

All new mutating P4 routes require the existing desktop JSON and
cross-origin controls; browser mode remains read-only.

## P5 Agent capability and explicit dependency dispatch

P5 adds project-local task prerequisites and on-demand DSH dispatch.
**It does not introduce an unattended cross-provider scheduler.**

### Provider capability registry

- GET /api/orchestration/agents/capabilities distinguishes independently installed CLIs from desktop apps and proven execution-ready adapters.
- Only verified DSH headless JSON mode can auto-execute after explicit desktop authorization.
- Installed desktop WorkBuddy/ZCode are manual handoff integrations. Independent CodeBuddy Code CLI and ZCode Agent CLI may be detected through Windows CLI shims, but presence never means credentials or permissions were verified.
- CodeBuddy Code's official headless write mode may require a dangerously skipped permission prompt; Magpie never enables that flag or guesses at a sandbox bypass.
- Detection does not invoke a model or launch any coding task.

### Prerequisite graph and Git integration gate

- POST /api/orchestration/tasks/{id}/dependencies replaces prerequisiteIds with task revision CAS in one transaction; GET /api/orchestration/dependencies?project={id} lists project edges.
- Max 32 prerequisites; self edges, duplicates, cycles, cross-project links, stale revisions and edits to active/review/done task records are rejected. Unresolved Agent Runs prevent graph edits.
- GET /api/orchestration/tasks/{id}/readiness previews launch readiness.
- A downstream task can launch only when each prerequisite is marked done, has no active/unresolved Run, has a clean and self-reported passed checkpoint, still has the same clean Git HEAD, and that exact tested commit is an ancestor of the downstream Worktree HEAD.
- These conditions are rechecked by direct Start/Resume and batch dispatch. Git verification is limited to 25 seconds overall.
- Magpie does not merge or cherry-pick commits. Merely copying a summary is not code integration; cherry-picked changes with a different commit hash do not satisfy ancestry.

### On-demand batch dispatch

- POST /api/orchestration/projects/{id}/dispatch with maxRuns 1-3 explicitly starts only ready DSH tasks, each using a separate Worktree and normal revision/Run lease protections.
- Other Agent assignments and blocked/unintegrated tasks remain unchanged and appear in the skipped list with a reason.
- No automatic launch occurs on task completion, checkpoint creation, or status change. All batch starts require an explicit desktop click.
- Web/browser mode cannot mutate graph or dispatch processes; both routes use the existing desktop JSON and cross-origin safeguards.
- UI includes prerequisite editor, task readiness action, provider capability matrix and batch result.

### Verification boundary

Tests check graph cycles, cross-project links, stale revisions, missing/passing/dirty checkpoints, Git ancestry, unresolved Runs, batch limits, APIs and browser controls. A human-marked passed checkpoint is not independently verified CI. WorkBuddy and ZCode automatic adapters remain future work after real CLI/auth/permission validation.

## P6 ZCode CLI plan-mode adapter (opt-in)

P6 introduces a separately configured ZCode Agent CLI analysis adapter. It
does **not** authorize unattended ZCode code edits, CodeBuddy Code writes,
or use of either desktop app's login/session data.

### Distribution and setup

- GTR source checkout used for verification: zai-org/ZCode at a9fe401.
  Source is in D:\\magpie-p6-zcode-src, separate from the installed ZCode Desktop.
- The standalone CLI can be built with the pinned Node/pnpm workspace toolchain.
  The produced Node bundle is apps/zcode-cli/packages/cli/dist/zcode.cjs;
  official single-executable builds are named zcode-windows-x64.exe.
- Desktop UI provides Configure ZCode CLI. Only a user-selected absolute
  existing zcode.cjs or the official Windows SEA filename is saved in
  the local agent_cli_profiles table; paths outside that format are rejected.
- Verify ZCode CLI is a separate user-triggered check of --help and
  --version under a Magpie-owned isolated HOME. It checks arguments only;
  it does not authenticate with a model or claim the model is ready.

### Plan-mode execution

- A task explicitly assigned ZCode or ZCode CLI, with ready status and
  an isolated Git Worktree, is eligible only after a successful **explicit,
  per-task live-model canary**. A successful --help/--version probe alone
  cannot enable Run ZCode analysis plan.
- Direct launch uses the same P3/P4 process-tree owner, durable Run ID,
  revision gate, task dependency graph and log tail as DeepSeek Harness.
- ZCode is invoked without a shell, with argv array and --mode plan,
  --workflow-mode disabled, --disallowed-tools for Bash and file-edit tools,
  --output-format stream-json and --cwd pointing to the registered Worktree.
- Magpie creates a separate HOME, USERPROFILE, APPDATA, LOCALAPPDATA and
  ZCODE_DATA_BASE_DIR per task under its local orchestration data dir.
  The child receives a restricted process environment and does not inherit
  arbitrary provider keys or the ZCode Desktop session.
- A ZCode plan Run has adapter zcode-cli-plan and does not claim a DSH
  session ID. The run's code changes are never automatically merged.
- `--mode plan` is a tool-level permission mode, **not an OS sandbox**.
  Plugins or MCP tools may have separate security rules. Do not put
  credentials or private data in task prompts: the ZCode CLI currently
  requires --prompt as an argv value, visible to local process inspection.
- Users must independently configure/authenticate the task-specific
  isolated ZCode CLI profile and explicitly click Verify ZCode task model.
  The canary invokes a fixed non-secret prompt under the same task HOME
  and requires a successful stream-json result event. A failed model call,
  missing result or changed CLI binary fails closed. No responses or keys
  are saved: SQLite records only task ID, CLI SHA-256 and verification time.
  Verification expires after 15 minutes and must be repeated if the
  executable changes, the clock moves backward or the task is unresolved.
  The verified model result permits **plan-mode only**, not independent
  CI acceptance, code modification or uncontrolled subprocess execution.
  Canary processes use the same owned process-tree management with a
  45-second timeout and terminate their children on timeout.

### Other providers

- WorkBuddy Desktop does not expose a verified headless process adapter.
  Independent CodeBuddy Code CLI uses a separate install and authentication.
  Official headless file editing can require a high-risk bypass of
  permission prompts; Magpie never sets that bypass automatically.
- The existing DSH explicit run and batch dispatch semantics are unchanged.
  ZCode plan-mode Runs are not automatically included in DSH batch dispatch.

### Verification

Focused Go tests cover profile persistence/validation, provider probes,
cross-origin and browser-only API guards, task ownership, run status,
credential environment isolation, and shared DSH process regressions.
Browser tests cover the configuration, independent CLI probe, per-task
model verification and plan launch. Real CLI smoke under isolated HOME
failed with no selected model, so authenticated execution remains UNVERIFIED.
Full release acceptance is a separate gate.

## P7 loopback-only ZCode model selection

P7 adds a small direct setup UI for a **local, keyless OpenAI Chat Completions**
server such as LM Studio or another compatible runtime. It does not read
other agents' login state or expose a remote API key store.

### Configuration source of truth

- `GET/POST /api/orchestration/agents/zcode/local-model` is desktop-only;
  web mode cannot read/write it and cross-site writes are forbidden.
- The operator enters an OpenAI-compatible HTTP URL using exactly
  `127.0.0.1` or `[::1]`, an explicit port, the `/v1` path, and a model ID.
  Hostnames, cloud URLs, userinfo, querystrings, alternate paths,
  malformed ports or invalid IDs fail validation.
- Magpie writes only its own `isolated-zcode/magpie-local-provider.json`
  under the orchestration Store directory, using an atomic temporary file.
  This version-1 file contains `magpie-local-loopback`, the selected
  provider/model and the fixed non-secret `local-no-key` placeholder.
  It contains no real authentication token.
- The file is not the user's ZCode Desktop or standalone CLI profile.
  The child process uses `ZCODE_PERSONAL_PROVIDER_CONFIG_FILE` to select
  this path explicitly; it still has its own per-task HOME/USERPROFILE.
- A missing file means 'not configured'. A malformed, altered, symlinked,
  oversized or unreadable existing file is an error, **not** silently
  reset to defaults or overwritten. Configuration is not shared with
  unrelated agents or Magpie's model gateway.
- Changing the model clears all recorded ZCode plan model canaries.
  Active or unresolved ZCode plan Runs block config updates. A canary
  checks its executable+model fingerprint before and after calling the
  model to reject changes made while it was running.
- If no local model profile is configured, P6's explicitly isolated
  standalone CLI model configuration remains unchanged.

### Verification and limitations

- Source of the format: ZCode `provider-config-file-codec.ts`, personal
  provider configuration docs, and `ZCODE_PERSONAL_PROVIDER_CONFIG_FILE`.
- A live successful model verification still requires a running local
  model server. CLI build/doctor/help and a simulated model response
  do not prove actual model inference.
- The official ZCode CLI 0.17.1 help advertises `--json` but not
  `--output-format` even though the headless output format is supported.
  Capability detection accepts either marker, with a dedicated
  regression test; the existing stream-json invocation is unchanged.
- A developer-only Go integration test runs the **real** ZCode binary
  against an `httptest` OpenAI-compatible server, from a clean isolated
  Git Worktree, through Magpie's per-task canary and owned Run path.
  Enable with `MAGPIE_ZCODE_CLI_INTEGRATION=/absolute/zcode.cjs`.
  It is skipped when that environment variable is absent and never
  depends on an external paid model or any user credentials.
- No OS process sandbox, no unattended editing/merging, and no
  authenticated remote/third-party provider integration are provided.
  DSH and WorkBuddy behavior remains unchanged.

## P8: explicit local model server readiness

A configured model ID is not proof that an inference server is listening.
P8 adds an explicit **Check local model server** action alongside the
existing loopback URL and model ID editor. The action is diagnostic only
and does not authenticate a model, authorize a ZCode Run or start a server.

- The desktop-only, same-origin guarded POST
  `/api/orchestration/agents/zcode/local-model/check` requires a JSON
  `{confirm:true}` request. Web mode and cross-site requests are rejected.
- The implementation in `internal/orchestration/zcode_readiness.go`
  resolves the already saved/validated model selection and requests only
  `GET /v1/models` from exact `127.0.0.1` or `[::1]` with an explicit port.
  Proxy support, redirects and alternate network hosts are disabled.
- Each request has a 3-second overall deadline, a response-header
  deadline, no keep-alive, and a 64 KiB response-size limit. Responses
  must contain a JSON `data` model array. Matching requires exact
  `id` equality; no streamed model output, credentials, or third-party
  network services are needed.
- Result fields `configured`, `reachable`, `modelFound`,
  `availableModels`, and `status` distinguish `unconfigured`,
  `unreachable`, `unexpected_response`, `model_missing`, and `ready`.
  Raw HTTP response bodies or arbitrary server strings are never
  displayed to the user or persisted.
- Successful readiness checks **do not** insert records into
  `zcode_plan_verifications`; the P6/P7 task-specific, expiry-bound
  live model canary remains mandatory before every plan-mode run.
- The installed WorkBuddy/WorkBuddyAI desktop runtime is not
  automatically treated as CodeBuddy Code's independently authenticated
  headless CLI. P8 does not alter the existing conservative Agent
  Capabilities behavior or enable noninteractive permission bypass.

### Local readiness regression verification

`go test -tags nogui ./internal/orchestration ./internal/gui -run
'TestZCodeLocalReadiness|TestZCodeReadinessDesktopOnly'` exercises
success, exact model mismatch, malformed/missing JSON, oversized bodies,
server errors, rejection of redirects and proxies, Web/cross-site API
guards, and confirmation that readiness is not a model-canary record.
The Projects UI workflow is checked in both Chromium and WebKit.

## P9: independent CodeBuddy Code CLI (manual handoff only)

WorkBuddy and WorkBuddyAI Desktop do not automatically authorize CodeBuddy
Code CLI execution. P9 adds explicit desktop-only configuration and a
version/help probe for an **independently installed official npm package**.
Magpie does not install the CLI globally, copy Desktop credentials, or
treat a successful help probe as permission to execute the agent.

- The registered path must resolve to a regular `bin/codebuddy` file
  inside a package with `package.json` name
  `@tencent-ai/codebuddy-code`. Desktop exe paths, packaged
  `@genie/agent-cli`, arbitrary .cmd/.bat shims, missing binaries and
  wrong package metadata are rejected.
- `GET/POST /api/orchestration/agents/codebuddy/config` allow the
  desktop user to save and inspect this path in the same local SQLite
  `agent_cli_profiles` table used by the ZCode adapter.
- `POST /api/orchestration/agents/codebuddy/probe` needs explicit
  `{confirm:true}` and desktop same-origin permissions. It runs only
  `--help` and `--version` under a separate Magpie-owned temporary
  HOME/APPDATA/TEMP directory and restricted process environment.
  The executable is invoked using Node directly and an owned process
  tree with a bounded timeout and output. Magpie never passes
  `CODEBUDDY_IS_SANDBOX`, API keys, or other inherited provider credentials.
- The CLI probe checks advertised `--print`, `--allowedTools` and
  `--output-format` options. A successful check sets `cliFound=true`
  but **always** `autoRunnable=false`; there is no P9 task-run route.
- Projects UI offers the CLI path, separate capability probe and
  explanatory warning. Existing project/task handoff copy supports
  manual work in WorkBuddy/CodeBuddy and still preserves the
  one-writer-per-Worktree rule.
- The official CLI's headless documentation says tools requiring
  authorization need `-y` / `--dangerously-skip-permissions`.
  P9 intentionally never invokes either; no unattended filesystem
  read/write/command execution, login reuse or Git merge is enabled.

### Tests

The new Go tests check official package identity, invalid paths,
isolated credential filtering, no tool permission escalation,
capability help/version, and failure on missing flags. Desktop API
tests exercise confirmation and Web/cross-origin access guards.
Chromium and WebKit check the CodeBuddy setup/probe panel while
asserting no Run is started by probing.

## P10: isolated no-tools model diagnostic and Git handoff receipts

P10 extends the independent CodeBuddy CLI integration without opening
unattended code execution. An explicit desktop-only
`POST /api/orchestration/agents/codebuddy/model/check` with
`{confirm:true}` invokes a fixed, public canary prompt in a newly
created empty temporary directory and an isolated HOME/USERPROFILE/
APPDATA/TEMP environment. It never reads the registered project Worktree
or forwards Desktop credentials. The CLI is started in `--print` mode
with `--permission-mode plan`, `--tools ""`, strict empty MCP
configuration, `--no-session-persistence`, `--max-turns 1` and JSON
output. Required flags are validated using its live `--help`.
The owned process tree has a 28-second total deadline, bounded output,
and is stopped if the deadline expires. Only enumerated status values
are returned: `unconfigured`, `cli_unavailable`,
`unsafe_cli_contract`, `unverified`, `timed_out` or `model_replied`.
Full CLI responses and potential provider details are never returned
to the browser or saved in the database.

**Important:** even `model_replied` only means the process returned a
matching JSON result. It neither authenticates a particular account
nor proves model quality or OS sandboxing. It does not write a Run,
modify a task, or authorize tools, merges or unattended coding.
The GTR workstation had no Docker/Podman command available at P10
inspection; Plan mode and an empty workspace are not OS isolation.
Do not use untrusted project prompts or private data with this probe.

### Human Git handoff snapshot and acknowledgment

- `GET /api/orchestration/tasks/{id}/handoff` generates an on-demand
  read-only snapshot from the **current** registered Worktree HEAD,
  clean/dirty Git status, task version/assigned Agent, and prerequisite
  task checkpoints. It uses the existing readiness gate: all
  predecessors must be completed, have a latest user-reported passed
  checkpoint in a clean Worktree and their exact tested commits
  must be ancestors of the destination Worktree HEAD.
- The snapshot receives a SHA-256 fingerprint covering the task
  revision, assigned Agent, current Git HEAD, readiness/clean status,
  and exact latest predecessor checkpoint identity/summary/commit.
  This checksum is **not** a signature, build attestation, CI result
  or durable permission to execute an Agent.
- `POST /api/orchestration/tasks/{id}/handoffs` requires an explicit
  desktop acknowledgment with the task revision and fingerprint.
  The backend recomputes a fresh Git snapshot and rejects stale
  hashes, dirty/blocked Worktrees, unresolved Runs, unintegrated
  dependencies, invalid statuses and repeated acknowledgments.
  An append-only receipt contains the recipient derived from the
  assigned Agent (DeepSeek Harness, ZCode, CodeBuddy Code, or
  WorkBuddy Desktop), Git head, fingerprint and timestamp.
- `GET /api/orchestration/tasks/{id}/handoffs` retrieves the receipts.
  History is local to Magpie's private orchestration SQLite DB;
  a task with receipts cannot be silently deleted. No Git branch
  or Worktree is created, merged, reset or removed by these APIs.
- All three endpoints are desktop-only; POST requires same-origin
  JSON and explicit confirmation. The Projects page previews blockers,
  allows human acknowledgment only for a ready snapshot, adds the
  fingerprint to the copied handoff information, and shows receipts
  alongside earlier checkpoint and recovery records.
- Tests use disposable Git repositories to simulate DSH → ZCode →
  CodeBuddy handoff with a real commit per predecessor, user-reported
  test checkpoint and explicit `git merge --ff-only`. They also cover
  stale Git HEADs, dirty Worktrees, cross-task fingerprints, missing
  checks, duplicate receipts and Web/cross-site permission guards.

### P10 incomplete gates

On GTR, the actual official CodeBuddy Code CLI 2.180.0 was invoked with
the fixed no-tools prompt and an isolated HOME. The result was
`unverified`; no authenticated model reply was proven. This is not
an invitation to copy Desktop credentials or to bypass permissions.
Independent CI, full Go and browser suites, manual WebView2 testing,
OS sandboxing, authenticated coding and fully automated three-Agent
execution remain outside P10 acceptance.

## P11: independently checked GitHub CI evidence

On P10, a human-reported `passed` checkpoint and acknowledged handoff
did not imply an independent CI runner had tested the code. P11 adds a
**separate, optional CI proof**; a plain manual handoff remains explicitly
unverified until a matching GitHub result is checked.

- `.github/workflows/orchestration-gate.yml` runs a focused, no-production
  orchestration regression on Windows and Linux on relevant feature
  pushes/PRs; it checks Go formatting, Go vet, targeted orchestration
  tests and the Projects UI's JavaScript syntax. This is not the
  repository's entire release suite or WebView2 manual acceptance.
- `POST /api/orchestration/tasks/{id}/handoff/ci-check` needs desktop
  same-origin JSON `{confirm:true}`. It recomputes the live Git
  handoff, reads the task Worktree's `origin` remote and accepts only
  an explicitly formatted public `github.com` owner/repository.
  It calls **only** GitHub's fixed `api.github.com` check-runs and
  combined-status endpoints for that **exact 40-hex Git HEAD**.
  It does not read `gh` login state or provider tokens, use secrets,
  or send user prompts/source files to GitHub.
- Redirects are forbidden and responses bounded to 512 KiB, with a
  9-second overall request limit. The verifier evaluates the full
  GitHub check count (at most 100), each check-run's HEAD SHA,
  completion status and conclusion, and any legacy status contexts.
  Only non-empty, fully successful checks yield `verified`.
  Zero checks, pending or failed contexts, rate limits, non-GitHub
  remotes, SHA mismatches and oversized/error responses are fail-closed
  (`no_checks`, `pending`, `failed`, `unavailable`,
  `unsupported_remote` or `handoff_blocked`).
- A **verified proof record** is a separate append-only
  `agent_handoff_ci_proofs` SQLite row referencing a pre-existing
  manual handoff receipt. The operator explicitly calls
  `POST /api/orchestration/tasks/{id}/handoff/ci-proofs` with
  `{confirm:true,revision,fingerprint}`. The server checks live
  GitHub CI *again*, and recomputes the task Git snapshot before
  adding a record with the exact commit SHA, repository, fingerprint,
  check count and checked-at time. Duplicate receipts are rejected.
- `GET /api/orchestration/tasks/{id}/handoff/ci-proofs` shows proof
  history. The Projects UI deliberately displays manual handoff
  confirmation and independently verified CI records as different
  evidence. Check results are fetched **only after the user clicks**
  Check GitHub CI; there are no continuous remote API calls.
- This proof is a historical observation, not a digital signature,
  persistent Agent authorization, guaranteed CI quality or future
  branch protection. GitHub check runs can be re-run or edited; an
  eventual real Agent execution must recheck current project state,
  live CI, permissions, Worktree, and process lease. Missing CI
  evidence does **not** silently change the earlier manual-only
  handoff policy, but it can never be labelled independently verified.
- The Go implementation and tests are in
  `internal/orchestration/ci.go` and `ci_test.go`; tests use only
  `httptest` GitHub JSON fixtures, no live external network. Desktop
  permission guards and Chromium/WebKit UI checks are in existing
  orchestration tests.
