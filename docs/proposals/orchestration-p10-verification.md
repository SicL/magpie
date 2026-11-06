# Magpie P10 — isolated CodeBuddy model preflight and auditable Git handoff

Date: 2026-11-06
Branch: `feat/orchestration-p10-auth-handoff`
Base: `feat/orchestration-p9-codebuddy-cli` / `cc777ce`
Status: development milestone only, stacked Draft PR; not release-ready.

## Changes

- Added explicit CodeBuddy fixed-prompt, no-tools model preflight. The real official independent CLI is invoked in an empty temporary directory with a new HOME, restricted environment, `--permission-mode plan`, `--tools ""`, strict empty MCP configuration, `--no-session-persistence`, `--max-turns 1` and bounded JSON output. All required CLI flags are checked before execution.
- The temporary HOME is removed after the request. Only enumerated diagnostic statuses are returned; no raw CLI reply, token or session is saved. An owned Windows process tree is stopped on timeout.
- Added a desktop-only, confirmed, cross-origin guarded `POST /api/orchestration/agents/codebuddy/model/check` endpoint and Projects button. Even a matching response never creates an agent Run or permits tools/file access. This is not OS process isolation.
- Added `GET /api/orchestration/tasks/{id}/handoff` and GET/POST `/handoffs` for current Git HEAD, uncommitted changes, latest predecessor checkpoints, and manual acknowledgement bound to task revision and SHA-256 snapshot fingerprint.
- Existing prerequisite Git/test/Run gates are reused: a destination cannot be acknowledged while a predecessor is incomplete, its latest human-reported test checkpoint failed/dirty, the tested commit is not integrated, a Run remains unresolved, or the Worktree is dirty.
- Handoff receipts are append-only local metadata, have a recognized assigned Agent as recipient, and cannot be reissued for the same snapshot. They cannot launch anything, change branches, merge code, approve CI, or attest an Agent identity.
- A task with a handoff receipt cannot be silently deleted from orchestration metadata even if its Worktree is removed; the UI includes receipt history and the existing task-copy handoff now includes readiness/fingerprint.

## Real GTR checks

- Official CodeBuddy Code CLI version 2.180.0 from `D:\magpie-p9-codebuddy-cli` was exercised by an isolated model diagnostic with `MAGPIE_CODEBUDDY_CLI_INTEGRATION` configured. Result: **`unverified`**, not authenticated success, and no Run was created. The CLI output is intentionally not included in this report.
- GTR has no usable `docker`/`podman` command in PATH. The CLI's `plan` flag and disabled tools are not an operating-system sandbox.
- CodeBuddy Desktop and WorkBuddy desktop saved login states were not copied, probed or modified. No changes were made to any production logistics/TMS applications or databases.

## Verification matrix

| Check | Result |
| --- | --- |
| CodeBuddy fixed-prompt model canary fixture (all tools disabled, no keys, mock success/failure) | PASS |
| Real official CodeBuddy CLI isolated model preflight | EXECUTED, status `unverified` |
| Git handoff snapshot staleness / dirty state / human ack / duplicate guards | PASS |
| Disposable DSH → ZCode → CodeBuddy Git predecessor integration and reported test checkpoint chain | PASS (simulated human-controlled commit chain, **not live Agent execution**) |
| Desktop API confirmation + web/cross-site enforcement | PASS |
| Chromium / WebKit Projects workflows | PASS 2/2 |
| Windows / Linux targeted Go vet | PASS |
| Go focused previous DSH/ZCode/CodeBuddy regressions | PASS: `go test -p 2 -tags nogui ./internal/orchestration ./internal/gui -run 'TestHandoff|TestGitHandoff|TestCodeBuddy|TestOfficialCodeBuddy|TestZCode|TestOfficialZCode|TestRunService|TestResumeUsesOwned|TestDependency|TestOrchestration|TestDispatch' -count=1`, with official CLI integration paths set |
| i18n ownership and new P10 keys | 3/4 PASS; all P10 strings present; the two existing Routing/Usage ownership conflicts remain |
| Windows development preview executable | PASS: `D:\magpie-p10-artifacts\magpie-dev-p10-final-reviewed.exe`, 105893376 bytes, SHA256 `8ACD4F41FC1C7B0F920181367FEE6ADFEEAC5B498D197AFF9B5307A89954C2FD` |
| Full project Go/UI suite and native WebView2 manual acceptance | NOT RUN |
| CodeBuddy authenticated model response or authorized code execution | NOT VERIFIED |
| OS-level model sandbox and real three-Agent autonomous coding | NOT IMPLEMENTED / NOT VERIFIED |

## Safety, authority and next steps

- The model preflight is purely diagnostic, not an authentication process or credential store. A forged CLI response can look successful; the probe never authorizes unattended code execution. CLI process arguments contain only a fixed public prompt.
- Handoff receipts represent a local operator's explicit confirmation of a Git snapshot. SHA-256 binds its fields against accidental changes; it is not a digital signature or independent validation of the stated test result.
- Git state can change after a receipt is recorded. Any later actual Agent execution must recheck task version, live Worktree, original commit ancestry, real tests, and process/run leases; a receipt must never serve as a permanent launch grant.
- Next milestone: authenticated independent CodeBuddy model profile under a real OS sandbox, reviewed tool permissions, validated actual project work and downstream CI, plus full-suite and WebView2 regression.
- Keep this PR as Draft and stacked on P9; do not merge until release gates pass.
