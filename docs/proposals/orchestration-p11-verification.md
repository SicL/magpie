# Magpie P11 — independently checked GitHub CI evidence for handoffs

Date: 2026-11-06
Branch: `feat/orchestration-p11-ci-attestation`
Base: `feat/orchestration-p10-auth-handoff` / commit `1e221e3` (P10 Draft PR #8)
Status: development Draft, not production approval and not unattended Agent execution.

## Problem

P10 recorded human-reported checkpoint pass statuses and human-acknowledged Git handoff receipts, but GitHub returned **no checks reported** for Draft PR #8. Calling these independent CI results would be incorrect. The fork's GitHub Actions feature is enabled, so P11 adds a focused CI workflow and an evidence verifier explicitly tied to the exact handoff SHA.

## Implementation

- `.github/workflows/orchestration-gate.yml`: targeted no-production Go/vet/JS syntax checks on Linux and Windows for orchestration-related changes. It runs on relevant feature pushes and pull requests. This is intentionally narrower than the full Magpie release suite and never touches user TMS resources.
- `internal/orchestration/ci.go`: on explicit user command, recomputes the task's P10 Git handoff preview and resolves only a supported `github.com/owner/repo` Git origin. It requests the fixed public `api.github.com` check-runs and combined legacy-status endpoints for the exact **40-character Git HEAD**. No GitHub login, token, raw prompt, executable action or private repository metadata is imported.
- Validates SHA on every check run, total count <=100, completion and `success` conclusion, plus legacy statuses. Only one or more **fully green** checks can return `verified`. Zero checks, pending, failed, SHA mismatch, unexpected API response or unsupported remote never returns verified.
- Network uses a bounded client, no redirects, at most 512 KiB per response and strict GitHub URL parsing; untrusted JSON does not become instructions.
- `POST /api/orchestration/tasks/{id}/handoff/ci-check` is desktop-only and requires `{confirm:true}`. The result is read-only; it does not save a pass or authorize a Run.
- A separate explicit `POST /api/orchestration/tasks/{id}/handoff/ci-proofs` requires an **existing human receipt** and live successful GitHub checks on the exact same fingerprint, revision and Git SHA. Evidence is rechecked remotely immediately before appending an immutable, distinct CI proof row. Proof records contain source repo, check count, SHA, fingerprint and UTC verification time. `GET /.../ci-proofs` shows history.
- Projects UI distinguishes manual acknowledgment from independent CI and shows `no_checks`, `pending`, `failed`, `unavailable`, `unsupported_remote` and `verified`. A CI proof is not permanent authorization; later check re-runs/branch changes require fresh evidence and independent validation.
- Corrected an existing P10 flaky test that occasionally generated an unchanged hash when it intended to corrupt a fingerprint.

## GTR verification

| Check | Status |
| --- | --- |
| Parse official GitHub SSH/HTTPS remotes and reject unsafe paths | PASS |
| Offline httptest CI states: zero/pending/failed/mismatched/overlarge/redirect | PASS |
| Manual receipt prerequisite, exact SHA, duplicate and immutable proof | PASS |
| GUI desktop-only API, confirmation and Web-mode denial | PASS |
| Chromium / WebKit Projects CI check and evidence record | PASS 2/2 |
| Go focused orchestration/GUI and prior DSH/ZCode/CodeBuddy regressions | PASS (`go test -p 2 -tags nogui ./internal/orchestration ./internal/gui -run 'TestGitHubCI|TestParseGitHubCI|TestHandoff|TestGitHandoff|TestCodeBuddy|TestOfficialCodeBuddy|TestZCode|TestOfficialZCode|TestRunService|TestResumeUsesOwned|TestDependency|TestOrchestration|TestDispatch' -count=1`, with real standalone CLI fixture environment); new API same-origin check run separately |
| Windows / Linux targeted Go vet | PASS (both) |
| Four-language i18n ownership | 3/4 PASS; only the two existing Routing/Usage key ownership collisions |
| Windows developer binary | PASS: `D:\magpie-p11-artifacts\magpie-dev-p11-reviewed.exe` (105938432 bytes), SHA256 `457B8BAFB207004F7E17772C2FAEBEE06871E7A6567882B6C8A00F3022751B80` |
| GitHub-hosted Windows/Linux CI on P11 head SHA | PENDING (requires push) |
| Full Go/UI/WebView2 and real CodeBuddy authenticated sandboxed coding | NOT VERIFIED |

## Security and release boundaries

- Public GitHub check-runs are metadata from a remote provider. They are independently executed only when a real runner produced them, and success does not prove tests are adequate or free of malicious workflow definitions. The proof is a historical observation, **not** a signature, release approval, model identity, or Agent launch permit.
- For a private repository, missing credentials or rate limits yield `unavailable`; P11 never copies `gh` authentication from the desktop. Other Git hosts are explicitly unsupported.
- A manual-only receipt remains possible for non-GitHub projects, but it must not be labelled as CI-verified. No existing DSH/ZCode/CodeBuddy automatic execution gate is loosened.
- P10's real independent CodeBuddy model preflight still returns `unverified` on GTR and OS-level sandboxing is not available; CodeBuddy unattended file/command operations remain disabled.
- Full repository test suite, browser-wide UI and native WebView2 acceptance remain prerequisites before merging stacked PRs.
