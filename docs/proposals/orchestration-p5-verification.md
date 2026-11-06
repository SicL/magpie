# Magpie P5 - explicit DAG dispatch and CLI capability discovery

Date: 2026-11-06
Branch: feat/orchestration-p5-dispatch
Parent: feat/orchestration-p4-checkpoints at 13bf584
Release status: development preview / stacked Draft PR only.

## Delivered

- Task dependency graph in SQLite; revision-based replace, project-local edges, duplicate and cyclic graph rejection, max 32 prerequisites.
- Start and native DSH Resume now enforce dependency evidence. Prerequisite must be done, have no active/interrupted Run, have a latest self-reported passed checkpoint on a clean unchanged Worktree, and that tested commit must be a Git ancestor of the downstream task Worktree HEAD.
- Checked Git ancestry is not a claim that commits were cherry-picked, merged or tested by CI; Magpie never changes Git branches automatically to satisfy dependencies.
- Explicit desktop batch dispatch (1-3 DSH tasks); per-task skips preserve the task state and provide reasons, including runs deferred by the batch limit. Every started Run still uses P3/P4 CAS, process ownership, session and log controls.
- CLI capability endpoint distinguishes DSH headless execution from WorkBuddy/ZCode desktops and independent CodeBuddy Code/ZCode CLIs. Unverified CLIs are never executed and high-risk permission bypass flags are never enabled.
- Desktop UI supports provider capability matrix on user request, prerequisite checkbox editor, readiness checks, batched DSH dispatch summary and disabled in-flight controls.
- Browser/Web mode stays read-only, including batch dispatch and graph edits. No new arbitrary command-line execution endpoint was added.

## Verification

| Check | Result |
| --- | --- |
| Focused Go tests (orchestration and GUI) under isolated HOME/GOCACHE | PASS |
| Chromium + WebKit page flows (prerequisites, capability view, dispatch, earlier P1-P4) | PASS, 2/2 |
| Go vet (Windows focused packages) | PASS |
| Linux/amd64 cross-target Go vet | PASS |
| i18n-keys ownership (4 checks) | PARTIAL, 3/4; remaining exactly the two existing Routing/Usage conflicts |
| Windows desktop production-tag development build | PASS: D:\magpie-p5-artifacts\magpie-dev-p5-reviewed.exe, SHA256 0C7C0EAE9DC10BC2023D78041F755FC0210CDB93D14236216A0807DFBC4A98C9 |
| Full Go suite / full browser suite / WebView2 manual acceptance | NOT RUN in P5 |
| WorkBuddy or ZCode authenticated headless coding execution | NOT IMPLEMENTED/NOT VERIFIED |

## Real provider findings

GTR has DeepSeek Harness CLI 0.1.7-rc.2 with headless JSON session protocol, already tested in P3/P4. WorkBuddy Desktop 5.9.3 and ZCode Desktop 3.17.2 are installed, but there are no verified standalone noninteractive CLI shims on PATH. The independent CodeBuddy Code headless documentation (https://www.codebuddy.ai/docs/cli/headless) requires potentially dangerous permission bypass for file/command tools. The independent ZCode CLI (https://github.com/zai-org/ZCode/blob/main/apps/zcode-cli/docs/usage/01-quickstart.md) needs a separate verified binary, credentials and permission configuration. Neither is automatically enabled by desktop app discovery.

## Safety boundaries

- The on-demand dispatch is not a background/cron scheduler. Users explicitly click the desktop control; no task completion event triggers another Agent.
- Worktree is a Git isolation mechanism, not a process security sandbox. DSH retains OS account privileges; only user-authored tasks should be launched.
- Latest checkpoint tests are self-reported, not CI-verified. Checkpoint Git SHA is required to be an actual ancestor of downstream isolated Worktree; manually merging or branching remains the operator's responsibility.
- Readiness is a point-in-time snapshot, rechecked during Start; concurrent independent Git mutations can still race without an OS filesystem sandbox.
- Model requests/commands from CodeBuddy or ZCode CLI were not issued. A standalone CLI's existence does not mean desktop login credentials or permissions are shared.
- Full upstream/regression acceptance gates remain pending. Prior clean upstream comparison reproduced two i18n ownership conflicts and Windows shim test failure; these must be treated separately from new P5 regressions.

## Next steps

1. Validate independent CodeBuddy Code and ZCode Agent CLI installation/authentication in isolated sandboxes; only then add provider-specific adapters with explicit permissions and process-tree supervision.
2. Integrate true CI/test gates and remote Git merge requests, rather than relying on a human-reported passed checkpoint.
3. Add multi-supervisor ownership and crash recovery; approval/lease-based unattended scheduler remains disabled until safe.
4. Finish full WebView2 desktop and full suite acceptance before shipping or merging stacked PRs.
