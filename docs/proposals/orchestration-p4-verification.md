# Magpie P4 - native DSH continuation, checkpoints and audited recovery

Date: 2026-11-06
Branch: feat/orchestration-p4-checkpoints
Parent branch: feat/orchestration-dsh-adapter
Status: development preview; do not merge/release until acceptance gates pass

## Scope implemented

- Task-scoped Checkpoint storage in SQLite with user-entered summary, test outcome (self-reported), Git HEAD and dirty/clean flag. Checkpoints reference only Runs from the same task and identical Worktree.
- Read-only checkpoint and recovery histories through dedicated HTTP endpoints.
- DSH native session continuation using --session-id from the latest terminal Run of the same task and Worktree. The client supplies a source Run ID, never an arbitrary DSH session string. A new Run has resumeOf linking to the original Run.
- Latest checkpoint summary included in DSH continuation prompt via stdin, not in command-line arguments or Magpie log.
- Interrupted-run reconciliation requires user attestation, audited reason, current task revision, conservative OS process absence check and Git Worktree inspection; task remains blocked after reconciliation. No automatic restart or process termination.
- Desktop UI: checkpoint form, history, manual cross-Agent clipboard handoff, DSH resume, and interrupted Run review. Browser mode is read-only.
- JSON-only and cross-site restrictions reused for all new mutating endpoints.
- Session ID parsing restricted to DSH stdout (not stderr), with direct regression test.

## Verification

| Check | Result |
| --- | --- |
| Go focused tests: internal/orchestration and internal/gui under nogui | PASS, includes checkpoint, native resume, recovery, process probe, API, P1-P3 regressions |
| Windows process liveness check | PASS, live current PID must stay blocked; absent PID permitted only with verified operator attestation |
| DSH native CLI smoke from isolated test Git repository | PASS: fresh session exit 0, resume same recorded session ID exit 0, structured events and final output both available |
| Windows batch CLI resume argv test | PASS: prior session ID forwarded as --session-id via args, latest checkpoint included on stdin |
| stdout/stderr session spoof test | PASS: stderr cannot overwrite stored session ID |
| Chromium and WebKit UI regression | PASS, both engines cover checkpoint, handoff, resume, recovery and narrow viewport |
| Windows target Go vet | PASS |
| Linux/amd64 target Go vet cross check | PASS |
| Windows desktop dev binary | PASS: D:\magpie-p4-artifacts\magpie-dev-p4-reviewed.exe |
| Full Go suite and full browser suite | NOT RUN in P4, still blocked from release acceptance |
| i18n ownership check | PARTIAL: 3/4 pass; only the two existing Routing and Usage conflicts reproduced on clean upstream remain |
| Manual desktop WebView2 acceptance | NOT RUN |
| Linux/macOS process liveness runtime test | NOT RUN |
| Race detector | NOT RUN; GTR Windows environment has no working C compiler |
| WorkBuddy/ZCode CLI process execution | NOT IMPLEMENTED: only manual handoff is validated |

Windows dev binary SHA256:
A2BA2BDAE8A4672E573F37590502021F3578A6C3D31804D9CA19705748CC3D63

## Known inherited baseline failures

Clean upstream Magpie checkout at baf7099 reproduced the same unrelated
Windows TestHowInstalledWindowsShim failure and i18n-keys duplicate references:
Routing "No session ID" and Usage "View routing". These are genuine baseline
reds, not new P4 behavior. They still need resolution before release.
Do not claim the full suite has passed.

## Boundaries and risks

- Worktree is Git checkout isolation, not a security sandbox. A coding Agent
  retains the logged-in user's system permissions.
- Checkpoint test status is user-reported; Magpie has not run or validated CI.
- A source Run must be latest and terminal, with validated native session ID
  from the same task and Worktree; interrupted/reconciled Runs cannot resume.
- PID absence does not prove all descendants are gone; the operator must
  explicitly verify it. An uninspectable or reused PID blocks recovery.
- Recovery writes an audit row and blocks the task. It never launches an
  Agent or merges code.
- Running multiple Magpie supervisors against one local Run database needs
  a dedicated ownership/locking design; second-instance reconciliation is
  not implemented in this phase.
- Large Git Worktrees may make synchronous status inspections slow; bounded
  Git execution and worker leases are later optimization work.
- Copy handoff transfers contextual project information only on user's
  explicit click; clipboard contents are sensitive.

## Provider capability check

GTR versions verified: WorkBuddy 5.9.3 and ZCode 3.17.2.
Their desktop installations have not exposed a proven headless CLI in PATH.
Separate documented CLIs exist:
- https://www.codebuddy.ai/docs/cli/headless
- https://github.com/zai-org/ZCode/blob/main/apps/zcode-cli/packages/cli/src/run.ts
Do not equate existence of a separate CLI source with desktop credentials,
authorization, or readiness for unattended automation. P4 keeps them manual.

## Next stage

- Test and document actual desktop WebView2 flows under isolated Magpie
  configuration. Never overwrite the user's original Magpie settings.
- Verify a supported ZCode CLI build/install and CodeBuddy Code CLI,
  inspect their permissions and authentication, then implement dedicated
  guarded adapters if available.
- Add background supervisor ownership/heartbeats, proper process and
  Worktree recovery, task dependency graph, and integration test/CI gates.
