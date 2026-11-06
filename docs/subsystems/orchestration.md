# Projects & Tasks orchestration subsystem

> Implemented scope: P1 project/task registry and P2 manual Git Worktree creation (not Agent execution).
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
