# Changelog

All notable changes to this project are documented in this file.

## [0.3.1] - 2026-02-15

### Added

- CLI regression tests for launchd plist path resolution to ensure runtime service labels are used as canonical source.
- `failover` regression tests for:
  - existing target settings-binding mismatch rejection before mutation
  - rollback cleanup of new gateway target launchd/plist artifacts
- Concurrency/atomicity regression tests for:
  - expected-active guard enforcement in `use`
  - `model switch` concurrent active change rollback safety
  - `failover` concurrent source-active change rollback safety
- `ccb preflight --from <scope> --to <scope> [--model <name>] [--json]` transition gate command to classify blocking checks vs advisory warnings before failover.
- `ccb handoff create --from <scope> --to <scope> [--model <name>] [--output <path>] [--json]` bundle command for operator/agent transfer with recommended `preflight -> failover -> doctor` sequence.
- CLI tests for preflight and handoff bundle generation.

### Changed

- `failover` documentation now reflects actual runtime-aware behavior (target scope runtime mode respected; gateway targets include runtime prep).
- Existing failover targets are now validated (policy/settings binding/provider/runtime constraints) before bootstrap mutation.
- `model switch` now verifies active pointer/generation contract at transaction tail before commit success.
- `failover` switch stage now enforces an expected-active contract at switch time (source-active atomicity guard).
- Docs now include context-gap mitigation flow (`preflight`, `handoff create`) for Codex->Claude fallback operations.

### Fixed

- Hardened launchd plist path handling in service lifecycle (`cleanup/service install/uninstall`) by deriving plist paths from runtime labels instead of potentially stale environment-derived filenames.
- Hardened failover rollback consistency:
  - newly created gateway targets now remove launch agents/plists during rollback
  - existing gateway targets now restore prior service install/start state after rollback
- Prevented rollback-time active pointer clobbering under concurrent switches:
  - `model switch` rollback restores active pointer only when transaction still owns target active scope
  - `failover` rollback restores active pointer only when active scope is still the failover target

## [0.3.0] - 2026-02-15

### Added

- `ccb service reconcile --vendor <v> --profile <p>` command for one-shot service recovery (`install -> start`) after partial launchd/runtime failures.
- `ccb setup --interactive` guided prompts for vendor/profile/runtime/model/settings-layer/backend selection.
- `ccb status --json` machine-readable status output for automation and trust checks.
- `ccb doctor --verbose` detailed diagnostics/history mode.
- Codex policy guard evaluation package (`internal/policy`) with strict enforcement and policy diagnostics.
- Doctor checks now include `policy guard` and `model proof`.
- New cross-vendor roundtrip regression coverage (`codex -> claude -> codex`) in CLI tests.

### Changed

- `service start` now auto-recovers once via reconcile path for known recoverable launchctl/start signatures.
- Setup and model-switch recovery paths now standardize on service reconcile behavior for recoverable start failures.
- `failover` target bootstrap now honors existing target scope runtime/profile config and supports gateway targets (excluding cleanup-only target mode).
- Status output now surfaces active scope, effective settings target, route/model proofs, policy mode, and policy guard state.

### Fixed

- Strengthened active scope switch contract checks (scope + generation consistency) for `model switch` and `failover`.
- Codex policy guard is now strict-only (no explicit compat bypass for codex scopes) and blocks high-risk settings/auth boundary misconfigurations with `ERR_POLICY_VIOLATION`.

## [0.2.4] - 2026-02-15

### Fixed

- Hardened launchd agent installation rollback semantics: `InstallAgents` now performs best-effort cleanup (bootout + plist removal) when any write/bootstrap step fails, so `service install` no longer leaves partial sync/proxy registrations on mid-step failure.
- Added launchd regression coverage for proxy-bootstrap partial failure, verifying cleanup of both plist files and rollback bootout calls.

## [0.2.3] - 2026-02-15

### Fixed

- `setup` now accepts `--settings-layer user|project|local` and `--settings-path <path>` so project isolation can be configured in one-shot flow without separate `bootstrap`.
- Added `doctor` settings-target validation for scope/cwd drift (`settings_layer=project|local` with mismatched `settings_path`) and surfaced the same check in `ccb status`.
- `claude apply` and `use` now hard-fail on project/local settings binding drift to prevent accidental cross-project settings mutation when the same scope is invoked from a different cwd.
- `proxy install --version` and setup proxy version inputs now accept both `vX.Y.Z` and `X.Y.Z` tags (while still supporting `latest`), reducing release-tag input failures.

## [0.2.2] - 2026-02-15

### Fixed

- Default Claude settings target for new scopes is now project-local (`settings_layer=project`, `<cwd>/.claude/settings.json`) to prevent proxy routing from leaking into other local projects/sessions.
- `bootstrap --settings-layer <layer>` now re-resolves default `settings_path` automatically when `--settings-path` is omitted, so legacy user-layer scopes can migrate cleanly to project isolation.
- `scripts/install_ccb.sh --source` now resolves its default source directory from the script's repository root (not caller `pwd`), so absolute-path invocation from other project directories no longer fails with `go: cannot find main module`.

## [0.2.0] - 2026-02-15

### Added

- Model normalization utility for vendor-aware canonicalization (`internal/model/normalize.go`).
- `setup --model` support for one-shot setup model selection.
- `ccb model switch --vendor <v> --profile <p> --model <name>` one-shot model transition command (active-scope-only, service running guarantee, doctor validation).
- Runtime mode split with explicit `native-cleanup` and `native-direct` semantics.
- New `claude` provider bundle and vendor-scoped direct path support.
- `ccb failover --from <vendor:profile> --to <vendor:profile> --model <name>` one-shot cross-scope failover transaction.
- Integration coverage for Codex fallback scenario (`codex:default -> claude:default`) with active generation/state/settings verification.

### Fixed

- Model-switch rollback now restores pre-switch config/state/active pointer/Claude settings on post-switch failures, and attempts previous-model service reinstall/restart during compensation.
- Proxy config generation now tunes Codex reasoning effort by model (`gpt-5.3-codex`=`xhigh`, `gpt-5.3-codex-spark`=`medium`) and sets `parallel_tool_calls=false` to reduce malformed empty-input tool-call bursts in gateway mode.
- Updated docs (`README.md`, `README_ko.md`, `CLAUDE.md`) for model selection flows (`setup --model`, `model switch`) and history-only `Past errors` interpretation.
- `vendor=codex` now rejects Claude selector models (`claude-*`, `opus`, `sonnet`, `haiku`) at normalization time to prevent invalid cross-vendor model switches that look successful but fail at runtime.
- `doctor` now includes a `model policy` check in gateway mode to detect and report invalid vendor/model combinations with a native-cleanup fallback hint.
- `doctor` now includes a `tool-call integrity` check that scans recent proxy transcript errors for empty-input/missing-parameter signatures and upstream auth/stream instability signals, with direct recovery hints.
- `use` now allows `runtime_mode=native-direct` scopes while continuing to block cleanup-only scopes.
- `doctor` now differentiates `native-cleanup` and `native-direct` checks/route-proof to avoid false positives in direct vendor mode.
- `failover` rollback now calls provider-level `Claude.Revert` instead of bypassing provider abstractions, preserving multi-vendor extension safety.
- `failover` now restores target scope config/state when target bootstrap fails, preventing partial target-scope drift on bootstrap-time failures.
- `ccb status` now surfaces `tool-call integrity` alongside route proof for faster user-visible diagnosis.
- `model switch` now fails fast with `ERR_INVALID_CONFIG` when proxy binary is missing, with direct recovery commands, instead of entering transaction rollback and surfacing noisy secondary launchctl errors.
- `model switch` rollback compensation now skips `service start` when rollback `service install` fails, reducing cascading error noise.
- CLI help detection now accepts `-help` in addition to `-h/--help/help` (e.g., `ccb model -help`).

## [0.1.0] - 2026-02-14

### Added

- Scoped vendor/profile architecture and active scope switching model.
- Agent-first install and verification scripts.
- GitHub Release workflow for darwin `amd64/arm64` artifacts with `checksums.txt`.
- `ccb setup` one-shot command for end-to-end scoped setup (`bootstrap -> proxy/auth/service -> claude apply -> doctor`).
- Backend abstraction layer (`internal/backend`) and default backend registry (`cliproxyapi`) with provider/backend separation.
- Experimental `builtin` backend with `ccb gateway serve --config <path>` runtime entrypoint.

### Fixed

- Default proxy artifact version now resolves to `latest` instead of a stale pinned tag.
- Service/proxy/use health paths now validate backend capabilities (`artifact/proxy/health`) independently from provider capabilities.
- Runtime mode split enforcement: `proxy/service` commands now hard-fail outside `runtime_mode=gateway`.
- `runtime_mode=native` is now cleanup-only: new scopes cannot bootstrap directly to native mode.
- `use` now rejects `runtime_mode=native` scopes to prevent treating cleanup scopes as active runtimes.
- `claude apply` in `runtime_mode=native` now removes ccgateway-managed proxy/model overrides instead of writing model settings.
- `claude apply` now updates active generation atomically with scope state when the target scope is currently active.
- `doctor` now performs mode-aware checks and prints direct recovery hints for missing proxy binary installs.
- `ccb service start` rollback on post-start healthcheck failure.
- Strict policy enforcement for mutating service commands (`--active` disallowed).
- Rejection of unexpected trailing positional arguments across CLI commands.
- `uninstall --purge` global port/active metadata updates under switch lock.
- Semantic-version archive selection for `install_ccb.sh --from-dist --version latest`.
- `verify_ccb.sh` artifact scope limited to `checksums.txt` entries.
- Active-generation guard to block unsafe cross-scope Claude revert/uninstall restores.
- Legacy active-pointer paths now require scope identity for settings mutation (prevents cross-scope revert when generation is empty).
- `service start/status` now hard-fail with capability error when backend health capability is missing.
- Runtime capability guard now validates declared capability has a non-nil implementation.
- `service start/stop` now fail with `ERR_STATE_WRITE_FAILED` when running-state persistence fails.
- `use` switch path now reports `ERR_ROLLBACK_FAILED` when revert/active-pointer rollback itself fails.
- Launchd label derivation now uses a consistent username fallback (`uid<uid>`) across CLI and scope path builders.
- `uninstall --purge` now updates global lock/active/ports state before removing scope files to reduce stale-global-state risk on partial failures.
- Bootstrap active-pointer initialization is now protected by switch lock to avoid first-run scope race conditions.
- `workflow_dispatch` release now builds from the resolved tag commit (`git checkout --detach refs/tags/<tag>`) to prevent tag/code drift.
- Bootstrap port reuse now requires scope launchd proxy presence plus backend health, preventing reuse of unrelated local `/v1/models` servers.
- `setup --runtime-mode native` now performs launchd proxy/sync agent cleanup and persists `service.running=false`.
- `uninstall --purge` now fails when active pointer cannot be read instead of silently ignoring malformed active metadata.
- Command failures are now logged with error code/context to scope/global `app.log`.
- Atomic file writes now use PID+timestamp temp file naming to reduce same-process collision risk.
- Native bootstrap no longer allocates/re-parses scope ports when `runtime_mode=native` (`proxy_enabled=false`), reducing cleanup-mode coupling to proxy port state.
- Service lifecycle cleanup/removal now prefers persisted state labels over recomputed username labels to avoid label drift issues.
- Repeated `claude apply` / `use` on an already-applied scope now preserves the original verified snapshot so `claude revert` restores the true pre-apply baseline.
- `doctor` no longer hard-fails native cleanup scopes for missing auth source files and no longer fails active-generation checks when Claude settings are intentionally clean (`applied=false`).
- `uninstall` now hard-fails with `ERR_ACTIVE_GENERATION_MISMATCH` when the target scope is active but `state.claude.applied_generation` does not match active generation, preventing silent snapshot loss and unrecoverable settings drift.
- `service start` now waits with bounded health-check retries before failing, fixing launchd start races that returned `connection refused` immediately after kickstart.
- Generated CLIProxyAPI config now includes `oauth-model-alias` mappings (`opus`/`opusplan`/`sonnet`/`haiku` and common Claude IDs -> configured Codex model), preventing `unknown provider for model opus` stalls after `claude apply`.
- Added Claude 4.6 alias coverage (`claude-opus-4-6`/`claude-sonnet-4-6`/`claude-haiku-4-6`) so those selector strings no longer fail with `unknown provider`.
- Updated docs (`CLAUDE.md`, `README.md`, `README_ko.md`) with explicit recovery playbooks for `ERR_SWITCH_VALIDATION_FAILED`, `ERR_LAUNCHCTL_FAILED` (`Could not find service`), and stale alias errors (`unknown provider for model claude-opus-4-6`).
- Documented post-upgrade requirement to run `service install` before `service start` so launchd/proxy config changes are applied.
- `setup` now suppresses redundant nested `bootstrap complete` output and auto-retries one-shot service-start failures (`Could not find service` / `connection refused`) via `service install -> service start`.
- `doctor` now supports `--clear-error-history`; report output relabels historical errors as non-blocking history with a clear-history hint.
- `doctor` now includes a `route proof` check so users can verify whether traffic is actually pinned to `claude -> local proxy -> backend/model` (gateway) or flowing through native Claude vendor path.
- Added top-level `ccb status` (alias `ccb ccb-status`) to show trust-focused runtime status (`route proof`) plus local proxy usage summary (`total/chat/count_tokens`, top paths) for the selected scope.

### Changed

- Project versioning compressed and normalized to `0.1.0` for all defaults, docs, scripts, and artifacts.
