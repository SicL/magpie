# Magpie P7 – local ZCode model configuration and real CLI integration

Date: 2026-11-06
Branch: `feat/orchestration-p7-local-model`
Base: P6 branch `feat/orchestration-p6-zcode-plan` / commit `36c1ed6`
Status: development preview; stacked Draft PR only. Do not merge or release.

## Observable changes

- In Projects > Configure ZCode CLI, users can now directly set a **local OpenAI Chat Completions endpoint** and a model ID, as well as the standalone ZCode CLI binary path.
- The new local model endpoint only accepts HTTP on **exact** `127.0.0.1` or `[::1]`, explicit port and `/v1` path. Remote providers, hostnames, injected credentials, query parameters and other paths are rejected. This feature accepts no API keys.
- Magpie serializes a ZCode v1 provider configuration to its own `isolated-zcode/magpie-local-provider.json` and explicitly passes the file to the child's `ZCODE_PERSONAL_PROVIDER_CONFIG_FILE`. It does not alter the user's ZCode Desktop or independent CLI login/config files.
- When no managed local profile exists, earlier P6 independent CLI configuration behavior is unchanged.
- The managed provider file is written atomically with restricted file mode. Malformed, manually changed, symlinked or oversized profiles are rejected and not overwritten. No secrets are present; the placeholder is a fixed non-secret string.
- Changing the selected model clears all prior canary results and is blocked when any ZCode Run remains active or unresolved. A canary checks combined CLI and provider file fingerprints before and after the request; a changed model requires a fresh verification.
- Fixed P6 CLI detection incompatibility: official ZCode 0.17.1 advertises `--json` in `--help` but not `--output-format`, although the latter is accepted in headless execution. The probe now accepts either structured-output marker. The actual `--output-format stream-json` process invocation remains unchanged.

## GTR runtime facts

- The ZCode CLI distribution from source `zai-org/ZCode` commit `a9fe401` (version `0.17.1`) was built at `D:\magpie-p6-zcode-src\apps\zcode-cli\packages\cli\dist\zcode.cjs`.
- Checked localhost endpoints `127.0.0.1:1234/v1/models`, `127.0.0.1:11434/api/tags`, and `127.0.0.1:8000/v1/models`; no running local model server was available at the time of testing.
- Standalone script test using the real official CLI and an isolated loopback OpenAI-compatible stub exited 0 and produced a structured successful `result`. The stub observed a chat request; this is protocol integration, **not real model inference**.
- Dedicated opt-in Go integration test runs the **actual CLI** from `MAGPIE_ZCODE_CLI_INTEGRATION` through Magpie Store, ZCode provider file, simulated HTTP model, explicit canary, isolated Worktree and supervised Run. This test does not contact a paid provider.
- Authenticated real-model inference, remote cloud providers, and WorkBuddy/CodeBuddy Code automatic execution remain unverified.

## Acceptance tests

| Check | Status |
| --- | --- |
| Local URL/model validation and corrupted profile rejection (Go) | PASS |
| CLI help contract regression and model-change canary invalidation (Go) | PASS |
| Official ZCode CLI + local httptest server + VerifyZCodeModel + Run (Go) | PASS (official ZCode v0.17.1, local stub, two requests) |
| Browser Projects model settings and P1–P6 regression, Chromium + WebKit | PASS 2/2 |
| Windows/Linux focused package Go vet | PASS |
| i18n ownership | 3/4 PASS, only the two pre-existing Routing/Usage conflicts |
| Windows dev executable build | PASS: D:\magpie-p7-artifacts\magpie-dev-p7-reviewed.exe (105803776 bytes), SHA256 C68D631BFFE85F9A29B5699CCEB9336B802D15EB349C0361F168BF7B4FF4708A |
| Complete Go test suite / full UI suite / Windows WebView2 manual acceptance | Full Go suite FAILED and was stopped after definitive failures; full UI suite (1,234 files) and WebView2 manual acceptance NOT RUN |
| Real local model or authenticated remote-model answer | NOT VERIFIED; no server running |

## Full Go suite blockers (not attributed to P7)

A separately executed `go test -p 2 -tags nogui ./... -timeout=100s -count=1`
from an isolated HOME failed before completion. The run was deliberately
stopped after these **definitive** failures to avoid unnecessary continued
load; the full suite is therefore red/incomplete, not green:

- `internal/agent`: `TestWorkBuddyRestartOnMac`,
  `TestWorkBuddyOwnSaveAfterStartIsNoChange`,
  `TestAsideOfflineDisconnectRestoresWithoutDaemon`, plus a timeout in
  `TestClaudeOffLeavesNoMagpieModel`.
- `internal/appdir`: `TestFoldersJoinedOnce` allocation assertions.
- `internal/browsermcp`: `TestImageFromLeavesTheRest` doubled Windows path.
- `internal/catalog`: `TestLivePathNamedOnce` allocation assertion.

Those packages were not changed in P7; causation was not independently
reproduced on a fresh base checkout in this turn. They remain release
blockers and should be separately investigated before merging stacked PRs.
## Safety / delivery limits

- Local loopback transport does not enforce an OS sandbox; the underlying CLI can still read or modify files based on operating-system permissions. Current Magpie execution is explicit plan mode with disallowed file-edit/command tools and its own HOME. No unattended editing or merges.
- The fixed `local-no-key` is not a provider credential. Users must separately start a trusted model server at the configured address and model ID. For local endpoints requiring authentication this feature does not apply.
- An external agent or local server is untrusted by default. Do not send production TMS data/secrets in prompts. The ZCode prompt is supplied in process arguments and may be visible to local process viewers.
- Canary success proves routing to a responding model, not model quality, tests, acceptance, Git integration or downstream CI results.
- The fork's previously observed upstream i18n/Windows baseline failures and incomplete full WebView2 acceptance remain release blockers. Draft PR does not mean release-ready.

## Remaining work

- Start a real local OpenAI Chat Completions model service (e.g., LM Studio) and explicitly select its exact exposed model ID in Magpie; then verify live inference using an isolated Git test repository.
- Verify WorkBuddy/CodeBuddy Code's own standalone noninteractive runtime under a separately authenticated, controlled sandbox. Do not enable `--dangerously-skip-permissions` on the Windows workstation.
- Implement independent CI/test approval evidence and review of downstream Git integration, plus robust multi-supervisor ownership, before permitting unattended multi-Agent relay.
