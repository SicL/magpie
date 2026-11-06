# Magpie P8 — local model readiness check and conservative CodeBuddy inspection

Date: 2026-11-06
Branch: `feat/orchestration-p8-model-readiness`
Base: `feat/orchestration-p7-local-model` / commit `b8e40ed`
State: Draft/development preview; NOT a release or authenticated multi-Agent automation.

## Why this change

P7 accepted a configured `127.0.0.1` or `[::1]` URL and model ID but did
not distinguish configured values from an actually listening local model.
P8 introduces a user-triggered, read-only `/v1/models` probe without
bypassing the existing P6/P7 live task-model canary requirement.

## Code and observable behavior

- `internal/orchestration/zcode_readiness.go` adds a bounded local
  OpenAI-compatible GET /v1/models check. Only exact saved loopback
  addresses are accepted. No proxy, redirects, or other hosts; 3s
  deadline and at most 64 KiB read.
- Results distinguish unconfigured, unreachable, unusable response,
  missing selected model, and selected model present. Only a count
  and enumerated status are returned, not the raw server response.
- Desktop-only, same-origin guarded and explicitly confirmed API
  `POST /api/orchestration/agents/zcode/local-model/check`.
- Projects configuration UI adds a model-server check action with
  status in all four supported languages and a reminder that even
  `ready` does **not** authorize an Agent Run.
- P8 does not start/stop an LM Studio or Ollama service, alter
  the model's settings, touch production TMS data, or reuse any login.

## Direct GTR capability checks

- Prior to testing, no listening service was detected on local ports
  1234 (LM Studio), 11434 (Ollama), 8000 and 8080.
- Standard LM Studio `lms.exe` and local model cache paths were absent
  from the inspected locations; no local model inference was performed.
- WorkBuddy and WorkBuddyAI GUI processes were running; they were not
  stopped or reconfigured.
- WorkBuddyAI `resources/app.asar.unpacked/cli/package.json` includes
  `@genie/agent-cli` and the `bin/codebuddy` entry. Under a separate
  empty HOME, its `--version` reported `2.161.0`, but `--help` failed
  because a `../dist/codebuddy` module is not present in that unpacked
  package path. This is **not** evidence of a functioning independent
  CodeBuddy Code CLI; no automatic executable registration was done.
- Official CodeBuddy documentation says noninteractive tool actions
  require an explicit permission policy. P8 intentionally does not
  call `-y`, `--dangerously-skip-permissions`, or set
  `CODEBUDDY_IS_SANDBOX` on the host.

## Validation matrix

| Check | Outcome |
| --- | --- |
| Go model readiness success/absence/error and local network restrictions | PASS |
| Desktop-only API confirmation and cross-site/Web mode rejection | PASS |
| Chromium / WebKit Projects flow including model check | PASS 2/2 |
| Windows + Linux targeted `go vet` | PASS |
| Go focused regressions including existing ZCode canary/Run | PASS (internal/orchestration + internal/gui; includes official CLI local-stub integration) |
| Standalone ZCode CLI official-binary local-server integration | PASS using official standalone CLI and httptest OpenAI-compatible server; **not** model inference |
| i18n keys across languages | 3/4 PASS; two known pre-existing Routing/Usage ownership conflicts (unchanged from P7) |
| Windows development binary | PASS: D:\magpie-p8-artifacts\magpie-dev-p8-reviewed.exe (105817088 bytes), SHA256 5451D4C0A4FD21E476C5AF2A41B68CC678D9E44290EFAB6F8BE59C286F7F320E |
| Full Go test suite / complete UI suite / WebView2 acceptance | NOT RUN; P7 full suite had unresolved unrelated failures |
| Actual local model inference | NOT VERIFIED; no model server found |
| Standalone CodeBuddy Code API execution | NOT VERIFIED; installed package has incomplete standalone entry |

## Boundaries and next steps

- A matching `/v1/models` response can be fabricated. Readiness is
  diagnostic only; even an affirmative model-list response does not
  prove inference quality, authentication, or Agent tool permissions.
- P7's explicit per-task model canary, Git Worktree, Run lease and
  hash/expiry guards remain the only supported ZCode plan launch path.
- Launch a trusted local model service explicitly, select the exact
  exposed ID in Projects and verify a real canary before any inference
  claims. Avoid using the desktop's saved credentials as a shortcut.
- For WorkBuddy Code, obtain a supported independent CLI distribution
  and validate plan-only authentication under an isolated HOME before
  even considering permission-approved write actions.
