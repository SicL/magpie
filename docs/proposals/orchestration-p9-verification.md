# Magpie P9 — official standalone CodeBuddy Code CLI capability probe

Date: 2026-11-06
Branch: `feat/orchestration-p9-codebuddy-cli`
Base: P8 branch `feat/orchestration-p8-model-readiness`, commit `1a4ee46`
Status: **development-only Draft**. The CLI help check is never authentication or permission to run tools.

## Goal and scope

Differentiate a supported independent `@tencent-ai/codebuddy-code` npm CLI
from WorkBuddy and WorkBuddyAI Desktop's bundled `@genie/agent-cli`.
Enable explicit CLI path registration and a safe isolated help/version
probe in the Projects UI, while keeping execution as **manual handoff**.

## Implementation

- `internal/orchestration/codebuddy.go` validates an absolute regular
  `bin/codebuddy` entrypoint nested within a `package.json` whose
  `name` is exactly `@tencent-ai/codebuddy-code`. It deliberately
  rejects desktop executables, missing/relative paths, .cmd/.bat
  wrappers, nonofficial manifests and unknown scripts.
- Uses the existing SQLite `agent_cli_profiles` table with the
  distinct adapter `codebuddy-code-cli`. No Provider tokens are stored.
- A desktop-only configuration GET/POST and explicit, confirmed POST
  `/api/orchestration/agents/codebuddy/probe` check only `--help`
  and `--version`, with an 8–12s bounded process-tree run, isolated
  HOME/USERPROFILE/APPDATA/TEMP and restricted environment. The
  full CLI output is not exposed or persisted.
- The probe requires `--print`, `--allowedTools` and
  `--output-format` in advertised help. If help fails or lacks
  any expected flag, `cliFound` stays false.
- Even when the CLI is detected, `autoRunnable=false`.
  The old PATH-based CodeBuddy probe was removed from the generic
  capabilities endpoint to prevent unrequested executable invocation.
- The Projects header offers a separate Configure CodeBuddy CLI panel
  with path, safe verification and warning. Existing task handoff
  copy remains available for human-operated CodeBuddy sessions.
- **No** automatic CodeBuddy Run, task dispatch, write action,
  login reuse, permission bypass or Desktop restart is added.

## Official CLI installation and real validation

- Official CLI installation guide recommends
  `npm install -g @tencent-ai/codebuddy-code` and reports
  `codebuddy`/`cbc` as entrypoints. To avoid modifying the user's
  current environment, P9 instead installs pinned version 2.180.0
  under `D:\magpie-p9-codebuddy-cli` with lifecycle scripts disabled.
- The official npm registry and the fallback mirror returned the same
  package metadata integrity value for version 2.180.0.
- This installation and its isolated --help/--version probe remain
  explicit acceptance gates; a partial npm extraction is not a
  successful CLI installation.
- Official headless documentation says authorized file/command/network
  tools in -p mode require -y/--dangerously-skip-permissions. P9
  NEVER enables those flags and never sets CODEBUDDY_IS_SANDBOX.

## Verification

| Check | Status |
| --- | --- |
| Go official-npm identity/path validation | PASS |
| Go fake CLI isolated help/version and API-key filtering | PASS |
| Desktop API confirmation + Web-mode/cross-site guard | PASS |
| Chromium / WebKit Projects panel & no auto Run | PASS 2/2 |
| Official standalone CLI installed and probes on GTR | PASS — @tencent-ai/codebuddy-code 2.180.0 from official-compatible SHA512 mirror, isolated `--help`/`--version` test 11.44s |
| Windows + Linux Go vet | PASS both targets |
| i18n all four languages | 3/4 PASS; only two previously known Routing/Usage ownership conflicts |
| Windows developer preview executable | PASS — D:\magpie-p9-artifacts\magpie-dev-p9-reviewed.exe (105842688 bytes), SHA256 6541BCA4EAB6D4971307F79305F9DAC468ADA3DD6165D69695EF6ECD92E42A66 |
| Complete Go/UI suite and WebView2 manual acceptance | NOT RUN |
| CodeBuddy authentication, reasoning and file/command execution | NOT VERIFIED |

## Current release blockers

- No unattended multi-Agent CodeBuddy write adapter exists, and
  the user's Desktop session is intentionally not consumed.
- P8's known i18n key ownership conflicts and failed full Go suite
  remain release blockers until independently fixed and verified.
- The local WorkBuddyAI packaged CLI cannot replace the official
  distribution because its unpacked `--help` lacked a required bundle.
- A valid --help/version result is not evidence that CodeBuddy can
  read code, answer model prompts or write safely. Keep PR Draft.
