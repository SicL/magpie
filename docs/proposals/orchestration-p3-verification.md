# Magpie P3 / DeepSeek Harness execution adapter - verification

Date: 2026-11-06
Branch: feat/orchestration-dsh-adapter
Base: feat/orchestration-p1-p2 at 6a292e9

## Delivered

- DeepSeek Harness headless execution adapter using the installed CLI contract (headless --json -); no arbitrary executable/arguments accepted from HTTP.
- Desktop-only explicit start, local run supervision through Magpie internal/proc.StartTree, owned-tree stop, up to 3 simultaneous runs.
- SQLite Run lifecycle: starting / running / stopping / completed / failed / cancelled / interrupted, task revision check, unique active or uncertain lease.
- Real DSH sessionId captured from structured session event and persisted. Resume by session ID is not wired yet.
- Prompt delivered via stdin, not command-line arguments and not included in Run log.
- Local bounded run log with best-effort credential redaction, desktop-only log tail access.
- Cross-site/null-origin/incorrect JSON-type rejection for process actions.
- Desktop Projects/Tasks page shows capability, run status, native session ID, exit code, log and stop button; browser/Web mode remains read-only.
- Go tests for run success/failure/stop, concurrent claims, supervisor restart uncertainty, session migration, redaction, unavailable logs, and Windows .cmd shim process tree.

## Verification evidence

| Check | Result |
| --- | --- |
| GTR actual DSH CLI: dsh headless --help | PASS: --json, --session-id and stdin task contract confirmed, version 0.1.7-rc.2 |
| GTR actual DSH headless JSON smoke in isolated temporary Git repository | PASS, exit 0, 7 events including session{sessionId,cwd}, status, text and final |
| Go focused tests: internal/orchestration and internal/gui with -tags nogui and specific Test selectors | PASS |
| Windows fake dsh.cmd shim: normal exit and stop child process | PASS |
| Concurrency: exactly one claimant for the same task/revision | PASS |
| Go vet -tags nogui: internal/orchestration, internal/gui | PASS |
| Playwright Chromium/WebKit: project, task, Worktree, agent run, log, stop and narrow viewport | PASS, 2/2 |
| Windows desktop development binary build, production tag | PASS, file at D:\magpie-p3-artifacts\magpie-dev-p3.exe |
| Full Magpie Go test suite | NOT RUN in P3; earlier P2 full run had unrelated failures and timeout |
| Full Playwright suite, full GUI/manual desktop acceptance | NOT RUN |
| Go -race | NOT RUN: Windows CGO_ENABLED=0, no GCC on GTR |
| Automatic DSH session resume and interrupted process reconciliation | NOT IMPLEMENTED |
| WorkBuddy and ZCode executable adapters | NOT IMPLEMENTED |

Windows binary SHA256:
4717941B092A2AE12F4EE90B39DC5FD79E5E7D0F124AE1622BDDBAF76CCA11B2

## Confirmed pre-existing failures

A clean upstream checkout at baf70992 was created as an independent Git worktree. With the same GTR environment it reproduced:
- Node i18n-keys: Routing key "No session ID" and Usage key "View routing" referenced outside their plugin.
- Go TestHowInstalledWindowsShim: incorrect Windows npm shim fixture expectation.

These tests failed without our P1-P3 code. They still count as unresolved baseline failures and are NOT an excuse to call the full suite green. No upstream files were modified to hide these failures.

## Security and limitations

- Worktree isolation does not restrict an Agent's OS permissions. The DSH process inherits the local user's privileges and must only run after an explicit desktop action.
- A resumed Magpie process marks unfinished older Run rows interrupted, leaving their task lease blocked. It never assumes an old process died and does not stop unknown processes.
- Log token redaction is best-effort only; tool output can still disclose project content. Local logs must be treated as sensitive.
- Browser/Web mode cannot start, stop or read logs. Process action routes require application/json and reject null/cross-site origins.
- The browser flow uses controlled API fixtures; the GUI developer binary was compiled but NOT manually launched or installed.
- The actual DSH CLI smoke was outside Magpie RunService; tests of RunService used isolated fake executables and the Windows batch shim to test ownership, output and exit.
- No direct production database or live TMS project was accessed; temporary fake Git repositories were used.

## Next development checkpoint

1. Real WebView2/desktop manual acceptance under an isolated Magpie configuration, including DSH capability view and user-triggered stop.
2. Add safe manual reconciliation for interrupted Run rows; never auto-unlock when PID status is unknown.
3. Add resume-by-native-session-ID and structured Change/checkpoint for cross-agent handoff.
4. Verify WorkBuddy and ZCode CLI or ACP support, then add their adapters.
5. Resolve upstream baseline regressions as separate changes before an upstream release.
