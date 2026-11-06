# P1/P2 Verification and Handoff - 2026-11-06

Branch: feat/orchestration-p1-p2
Feature commit: cd1e42697333b63c0d7f431b5512ad05707d4cd5

## Delivered

- Separate SQLite project and task registry, HTTP API and manual task status with optimistic revision checking.
- Git repository and base branch validation, explicit isolated task Worktree creation, Git-sourced listing and no automatic reset or deletion.
- Worktree ownership remains visible after detached HEAD; cannot remove task metadata while its Worktree exists.
- Built-in Magpie Projects page with Chinese, English, Traditional Chinese, Japanese and German localization.
- Desktop mutations only; browser mode is read-only in both UI and server.
- No automated Agent execution, lease-based scheduling, cross-Agent handoff or CI/MR automation in this version.

## Verified checks

- PASS: gofmt -l on changed Go files; git diff --cached --check.
- PASS: node --check for client.js, i18n.js and Playwright test.
- PASS: focused Go tests in internal/orchestration and internal/gui, including API, state revision and Worktree handling.
- PASS: new Playwright scenario in Chromium and WebKit: register project, create task, create isolated Worktree and narrow viewport.
- PASS: Chromium plugin-kernel and plugin-page smoke tests, 5 of 5 tests.
- PARTIAL: i18n-keys test, 3 of 4 checks passed. Remaining duplicate-key references are in unchanged routing and usage subsystem test files.
- FAILED/TIMED OUT: go test -p 2 -tags nogui ./... -count=1 ran for 600 seconds on Windows. Failures observed in unchanged agent subsystem tests, including TestHowInstalledWindowsShim, TestCodexSyncMendsGoneCatalog, TestCodexSignInInKeychain, and TestDesktopHandlerNeedsItsEntryAndProgram.
- NOT RUN: full go vet, cross-platform builds, full browser suite, old-code regression/mutation checks, real Agent process lifecycle.

These Go tests are in files not changed by the orchestration feature. A clean upstream baseline comparison has NOT been performed; do not claim the failures are confirmed pre-existing. The full suite is not accepted and this branch is not release-ready.

## Tool environment and cleanup

- GTR Windows Go 1.27.3; DeepSeek Harness dsh CLI v0.1.7-rc.2 has headless mode.
- WorkBuddy and ZCode desktop installations exist but their non-interactive CLI operation is not verified.
- First full Go test hit C drive low-space. Second run used D drive test home and temporary build/cache; disk problem did not recur, but existing tests failed/timed out.
- Temporary build directories created for these runs were removed after testing; compiler cache on D retained. TMS project files and production DB were not touched.

## Next checkpoint

1. Compare failing regression tests against clean upstream checkout using equivalent Windows test environment.
2. Implement DeepSeek Harness execution adapter with capability detection, task-scoped cwd, process ownership, stop/cancel, logging and redaction.
3. Check WorkBuddy and ZCode for real supported non-interactive entrypoints; use manual handoff when unavailable.
4. Build task leases, dependency graph, Checkpoint/Run records, cross-Agent handoff and CI/MR integration separately.
