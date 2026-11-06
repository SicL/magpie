# Magpie P6: explicit ZCode CLI plan adapter and live-model verification

Date: 2026-11-06
Branch: feat/orchestration-p6-zcode-plan
Base: feat/orchestration-p5-dispatch (commit 4dea4e7)
Status: developer preview; no merge or production release.

## Implemented

- ZCode standalone CLI configured using an absolute path to an existing versioned zcode.cjs bundle or official named Windows SEA executable. Neither the WorkBuddy nor ZCode Desktop login is reused.
- Reused P3/P4 owned process-tree launcher and durable Run records for both DSH and ZCode. ZCode adapter is `zcode-cli-plan`; DSH session recording and native resume remain specific to DSH.
- ZCode CLI `--help` and `--version` probes run under an isolated HOME and restricted process environment; a successful CLI help check never alone permits model execution.
- An explicit per-task Verify ZCode task model API invokes a fixed non-secret canary in `--mode plan`, disabled dangerous file/command tools, and requires a structured successful `result` event. Deadline 45 seconds; owned process-tree supervision stops descendants on timeout. Authorization expires after 15 minutes.
- A successful canary saves only task ID, SHA-256 of CLI executable and timestamp in `zcode_plan_verifications`, not prompt/response/tokens. Missing, stale, changed CLI binary, or uncleared Run lease fails closed.
- Only then can the user explicitly start a task assigned ZCode from its own registered Worktree with status ready and P5 predecessor Git integration gates satisfied. No unattended dispatch, writes or merges.
- ZCode process environment is limited to PATH/process startup essentials plus isolated HOME, USERPROFILE, APPDATA, LOCALAPPDATA, ZCODE_DATA_BASE_DIR, TEMP and TMP. User ambient API keys are not forwarded.
- Desktop project UI includes CLI config, CLI probe, per-task live model verification and ZCode plan launch, history and adapter label; new API mutations require desktop JSON/same-origin guards.

## Real official ZCode CLI evidence

- Source: https://github.com/zai-org/ZCode at commit a9fe401, cloned only to D:\magpie-p6-zcode-src.
- Filtered dependencies installed using pnpm frozen lockfile with --ignore-scripts in isolated home; versioned config restored from repository after sparse checkout.
- Standalone Node bundle built at D:\magpie-p6-zcode-src\apps\zcode-cli\packages\cli\dist\zcode.cjs (~30 MB). Version 0.17.1.
- `node zcode.cjs doctor --json` returned exit code 0 with platform/runtime info; this only validates CLI packaging.
- A real noninteractive model prompt under isolated HOME and no inherited user tokens exited 1 without a final result. Diagnostics included 'Select a model before sending a message' and a built-in provider revision/content conflict. No authentication or real coding success was shown.
- Therefore Magpie does NOT auto-enable this provider. An independently configured model is required; canary is deliberately required before each task's plan execution.

## Test matrix

| Check | Result |
| --- | --- |
| Focused Go orchestration/GUI tests with fake CLI (persistence, auth gate, digest, process lifecycle, DSH regressions) | PASS, isolated HOME; includes expired canary and changed-binary rejection |
| Chromium and WebKit UI tests (ZCode configuration, probe, verification, launch, P1-P5) | PASS 2/2 |
| Windows Go vet targeted packages | PASS |
| Linux/amd64 cross-vet targeted packages | PASS |
| i18n ownership tests | 3/4 PASS; only the two existing Routing/Usage conflicts on clean upstream remain |
| Windows development binary build | PASS: D:\magpie-p6-artifacts\magpie-dev-p6-reviewed.exe (105764352 bytes), SHA256 F6ED21F364DC3660F3D96C8458F77E5FBBE0539DD2481CD2E481B97EA9E10F94 |
| Full project Go test suite / all browsers / manual WebView2 acceptance | NOT RUN |
| Authenticated real ZCode model success | NOT VERIFIED (explicitly blocked) |
| Headless WorkBuddy/CodeBuddy Code execution | NOT IMPLEMENTED |

## Security and delivery limitations

- `--mode plan` is not an OS sandbox; untrusted plugins/MCP can have separate permissions. Magpie also supplies --disallowed-tools for file modifications and Bash. Do not use on live production projects or with production secrets.
- ZCode requires --prompt as command-line arguments, so process viewers could read task text. Never include credentials/private TMS data in task description.
- CLI executable hash establishes integrity relative to the verified canary, not code trust. An adversarial CLI can still lie about its results or perform other actions. This is a developer preview only.
- Latest tests/checkpoints are still user-reported, not independent CI pass. A successful model canary proves reachability only, not correctness or Git integration.
- Explicit action only: no automatic cross-Agent WorkBuddy/ZCode/DSH pipeline, no unattended write mode and no automatic Git merge.
- Full upstream known baseline i18n/Windows shim failures and all release acceptance gates remain pending; do not merge until resolved.

## Next checkpoint

1. Create an isolated, explicitly authenticated ZCode CLI model profile and verify a real canary without inheriting desktop credentials.
2. Evaluate OS-level sandbox and user-configured permission policy before opening ZCode edit/build mode.
3. Validate independent WorkBuddy/CodeBuddy Code CLI authentication and safe approval-mediated coding, or remain manual.
4. Full Windows WebView2 desktop acceptance, complete Go/browser suites, and PR review before any merge.
