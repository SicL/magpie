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
