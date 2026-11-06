# Magpie P12 — release CI hygiene and key-free Projects UI acceptance

Date: 2026-11-06
Branch: `feat/orchestration-p12-ci-ui-acceptance`
Base: `feat/orchestration-p11-ci-attestation` / `4f099ee`
Status: development milestone, not release approval.

## Origin of this work

P11's focused Linux/Windows Orchestration Gate was green, but the full
GitHub Test workflow on P11 failed in Linux because `plugins/all/all.go`
was not gofmt-clean, and macOS reported several new orchestration
and GUI plugin fixture failures. The upstream UI preview uses a
separately configured provider key that was not present on this fork.

## P12 changes

- Format the single `plugins/all/all.go` import-order error without
  changing the imported packages or behavior.
- Canonicalize the Worktree root at project registration with the
  existing symlink-aware `prospectivePath` method; reject symlink
  destinations resolving inside the registered repository *before*
  insertion. Keep Worktree registration read-only.
- Add a regression for dangerous and safe symlink-root aliases, and
  make macOS CodeBuddy fixture and Worktree tests compare canonical
  directory identities without weakening credential-isolation checks.
- Update GUI built-in count/Essentials preset expectations to account
  for the Projects plugin, and translate its public subtitle in
  all four global UI languages.
- Record each of the existing Projects GUI API routes explicitly in
  the CLI parity registry as not yet implemented as terminal commands.
  Future routes must still be reviewed before the test can pass.
- Add credential-free Playwright Chromium/WebKit Projects workflow
  to the Orchestration Gate, with fabricated API data and narrow
  screenshots uploaded for PR review; no model login is needed.

## Verification matrix

| Test | P12 status |
| --- | --- |
| GTR Windows focused Go: canonical project, Worktree, isolated CodeBuddy, built-ins translations and Essentials | PASS |
| GTR Windows CLI/GUI route parity test | PASS (44 Projects endpoints explicitly tracked as missing CLI counterparts) |
| GTR targeted DSH/ZCode/CodeBuddy, GitHub CI, Projects/builtins and CLI regressions | PASS across internal/orchestration, internal/gui and internal/cli (Go -tags nogui -count=1) |
| GTR Chromium & WebKit fixture UI (including screenshots) | PASS 2/2; screenshots saved under `D:\magpie-p12-artifacts\browser` |
| GTR Windows/Linux targeted Go vet | PASS |
| P12 Windows development binary | PASS: `D:\magpie-p12-artifacts\magpie-dev-p12-final-reviewed.exe`, 105938432 bytes, SHA256 `09B0CC6920211DDD7B6F539EB403F6D197731310B036631368A323EAAAEA45BD` |
| New GitHub-hosted Linux/Windows focused gate and Chromium/WebKit browser job | PENDING (requires push) |
| Full GitHub Test macOS, Linux and Windows | PENDING (requires push) |
| Legacy i18n ownership test | 3/4: two inherited Routing/Usage collisions |
| Secret-dependent full UI preview | NOT VERIFIED: fork CI environment is unconfigured |
| Native Windows WebView2 app acceptance | NOT YET VERIFIED |
| Independent CodeBuddy authentication, model reply or OS sandboxed edits | NOT VERIFIED; automatic editing remains disabled |

## Boundaries

- The new Playwright CI is fully fixture backed, so it proves only
  the authored Projects page behavior in browser engines, not a
  real Magpie desktop session or end-to-end remote model access.
- The original secret-bearing `.github/workflows/ui-preview.yml` is
  unchanged. P12 never supplies a placeholder as if it were an
  authenticated service credential, and never reads user logins.
- The broader macOS `internal/provider` test failure must be
  assessed separately; unverified unrelated behavior is never
  counted as a P12 pass.
- Keep this branch's PR stacked and Draft; do not merge or deploy.
