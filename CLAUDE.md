# Claude Agent Install Guide

This repository provides a deterministic install/verify flow for agents.

## Required workflow

1. Install only through `scripts/install_ccg.sh`.
2. Verify only through `scripts/verify_ccg.sh`.
3. Avoid ad-hoc `curl | bash` or manual binary replacement.
4. Do not ask users to run `bootstrap`, `proxy install`, `service`, `claude apply`, or `doctor` manually.
5. The agent must collect scope inputs first, then execute the full setup end-to-end.
6. After upgrading `ccg`, re-run `service install` for the target scope before `service start` so proxy config/plists are regenerated.
7. `setup` may auto-retry `service install -> service start` once for known service-manager failures (launchctl on macOS, systemctl on Linux) and connection-refused signatures; if it still fails, use the recovery block below.

## Initial install and setup guide

### 1) Prerequisites

- **macOS** (darwin `arm64` or `amd64`) **or Linux** (`amd64` or `arm64`)
- Claude Code installed
- Codex auth file available at `~/.codex/auth.json` (gateway mode); native cleanup mode does not require auth sync/source (custom path via `--auth-source`)

**Linux-specific prerequisites:**

- `systemd` (user session support required)
- `loginctl enable-linger <user>` — enables user services to run without an active login session
- `XDG_RUNTIME_DIR` set (typically `/run/user/$(id -u)`; auto-set on most systemd distros)

### 2) Install `ccg`

Recommended (agent-safe, deterministic):

```bash
./scripts/install_ccg.sh --source --install-dir "$HOME/.local/bin"
export PATH="$HOME/.local/bin:$PATH"
scripts/verify_ccg.sh --binary "$HOME/.local/bin/ccg"
```

`--source` mode builds from this repository root by default, so it still works when invoked via absolute script path from another working directory.

Or install from GitHub Release:

```bash
./scripts/install_ccg.sh --repo <owner>/<repo> --version latest --install-dir "$HOME/.local/bin"
export PATH="$HOME/.local/bin:$PATH"
scripts/verify_ccg.sh --binary "$HOME/.local/bin/ccg"
```

If you need `/usr/local/bin`:

```bash
sudo ./scripts/install_ccg.sh --repo <owner>/<repo> --version latest --install-dir /usr/local/bin
scripts/verify_ccg.sh --binary /usr/local/bin/ccg
```

Artifact checksum verification only (CI/release, no binary install):

```bash
./scripts/verify_ccg.sh --checksums ./dist/checksums.txt --skip-binary
```

### 3) One-shot setup (recommended)

```bash
ccg setup --vendor codex --profile default
ccg setup --vendor codex --profile default --gateway-backend cliproxyapi
ccg setup --vendor codex --profile default --gateway-backend cliproxyapi --settings-layer project
ccg setup --vendor codex --profile default --gateway-backend cliproxyapi --model gpt-5.3-codex-spark
ccg setup --interactive
# experimental (health-oriented) backend; not for full LLM routing
ccg setup --vendor codex --profile default --gateway-backend builtin
```

This runs `bootstrap -> proxy install -> auth sync -> service install/start -> claude apply -> doctor`.
If you only need native cleanup transition:

```bash
ccg setup --vendor codex --profile default --runtime-mode native-cleanup
```

Native cleanup setup also removes existing scope service agents (macOS: launchd, Linux: systemd) (`proxy`/`sync`) before applying cleanup settings.
For Codex, model aliases are normalized (`codex` -> `gpt-5.3-codex`, `codex-spark`/`spark` -> `gpt-5.3-codex-spark`).

Direct Claude scope setup (no local proxy route):

```bash
ccg setup --vendor claude --profile default --runtime-mode native-direct --model claude-sonnet-4-6
# or for max intelligence
ccg setup --vendor claude --profile default --runtime-mode native-direct --model claude-opus-4-6
```

If you upgraded `ccg` binary, run `ccg service install --vendor codex --profile default` once to regenerate service config (launchd plist on macOS, systemd unit on Linux) before `service start`.

### 4) Bootstrap a scope (manual path)

```bash
ccg bootstrap --vendor codex --profile default
```

Default behavior: `ccg` isolates Claude routing per project by writing to `<cwd>/.claude/settings.json` (`settings_layer=project`), so other local projects/sessions keep their original Claude path.

If an existing scope was created with old user-layer defaults, migrate once:

```bash
ccg bootstrap --vendor codex --profile default --settings-layer project
ccg claude apply --vendor codex --profile default
```

If the same `vendor/profile` is reused from a different project cwd, `setup`/`claude apply`/`use` can now fail with `settings_layer=project mismatch` to prevent cross-project settings writes. Rebind explicitly:

```bash
ccg setup --vendor codex --profile <profile> --settings-layer project
```

### 5) Install runtime dependencies for the scope

`gateway` mode (uses local proxy / CLIProxyAPI):

```bash
ccg proxy install --vendor codex --profile default --version latest
ccg auth sync --vendor codex --profile default
ccg service install --vendor codex --profile default
ccg service start --vendor codex --profile default
ccg service reconcile --vendor codex --profile default
```

`--version` accepts both `vX.Y.Z` and `X.Y.Z` (`latest` also supported).

`native-cleanup` mode (cleanup-only transition path from gateway):

```bash
ccg bootstrap --vendor codex --profile default --runtime-mode native-cleanup
ccg claude apply --vendor codex --profile default
ccg doctor --vendor codex --profile default
```

### 6) Apply Claude settings explicitly

```bash
ccg claude apply --vendor codex --profile default
```

No automatic settings mutation occurs before this command.
In `native-cleanup` mode, `claude apply` removes ccgateway-managed proxy/model override keys from Claude settings.

### 7) Validate health and status

```bash
ccg service status --vendor codex --profile default
ccg doctor --vendor codex --profile default
ccg status --vendor codex --profile default --json
```

### 8) Switch active scope (optional)

```bash
ccg use --vendor codex --profile default
ccg service status --active
ccg doctor --active
```

### 9) Switch model after install (active scope only)

```bash
ccg model switch --vendor codex --profile default --model codex-spark
# equivalent canonical form
ccg model switch --vendor codex --profile default --model gpt-5.3-codex-spark
ccg status --vendor codex --profile default
ccg doctor --vendor codex --profile default
```

`model switch` runs one-shot transition: `config update -> service install/start -> claude apply -> doctor`.
It now enforces active-scope expectation at apply time and verifies active generation at transaction tail to prevent concurrent-switch commit races.
The target scope must be the current active scope, and successful switch keeps the service running.
For `vendor=codex`, Claude selector models (`claude-*`, `opus`, `sonnet`, `haiku`) are rejected by policy.
`model switch` expects proxy artifact already installed for the target scope (`setup` or `proxy install`).

Codex quota/token exhaustion fallback (current supported path):

```bash
# disable gateway routing and clean ccgateway-managed Claude overrides
ccg setup --vendor codex --profile default --runtime-mode native-cleanup
ccg doctor --vendor codex --profile default
```

After native cleanup, select/use Claude-native model (for example `claude-sonnet-4-6` or `claude-opus-4-6`) directly in Claude Code path.

### 10) Cross-vendor failover (one-shot)

```bash
ccg preflight --from codex:default --to claude:default --model claude-sonnet-4-6
ccg failover --from codex:default --to claude:default --model claude-sonnet-4-6
# or --model claude-opus-4-6 for max intelligence
```

`failover` applies a transactional scope switch: target bootstrap/update (uses target scope runtime mode) -> target runtime prep (gateway only) -> active switch -> doctor validation -> rollback on failure.
For existing target scopes, settings binding/policy validation now runs before mutation; if binding is mismatched, failover is blocked without changing target config/state.
`failover` switch stage also validates the source active snapshot at switch time; if source active changed concurrently, failover aborts and rolls back without clobbering external active changes.

### 11) Context-gap preflight and handoff bundle

`preflight` is a failover gate that distinguishes:

- blocking checks (must pass before failover)
- warnings (advisory signals like route/tool integrity)

```bash
ccg preflight --from codex:default --to claude:default --model claude-sonnet-4-6
ccg preflight --from codex:default --to claude:default --model claude-sonnet-4-6 --json
```

For multi-agent/operator handoff, generate a ready-to-run bundle:

```bash
ccg handoff create --from codex:default --to claude:default --model claude-sonnet-4-6 --output /tmp/ccg-handoff.md
```

`handoff create` does not mutate scopes/services. It packages preflight checks and next commands (`preflight -> failover -> doctor`) into markdown or JSON.

### 12) Revert and uninstall (optional)

```bash
ccg claude revert --vendor codex --profile default
ccg uninstall --vendor codex --profile default
# full cleanup
ccg uninstall --vendor codex --profile default --purge
```

## Agent-first interactive setup (no manual step-by-step)

When the user asks to install/setup ccgateway, the agent should:

1. Ask configuration questions with **UserQuestion Tool** (not free-form guesswork):
   - `vendor` (recommended default: `codex`)
   - `profile` (recommended default: `default`)
   - `runtime_mode` (`gateway` recommended; `native-cleanup` is cleanup-only, `native-direct` for direct Claude scopes)
   - `model` (recommended default: `gpt-5.3-codex`; optional: `codex-spark` -> `gpt-5.3-codex-spark`; for `vendor=claude`: `claude-sonnet-4-6` recommended, `claude-opus-4-6` for max intelligence)
   - `settings_layer` (recommended default: `project` for per-project/session isolation; `user` only when global routing is explicitly desired)
2. Perform install + setup commands directly.
3. Return final status/result only after `doctor` succeeds (or return actionable failure).

### Suggested UserQuestion prompts

- "Which vendor should I configure?" (`codex` recommended)
- "Which profile should I create/use?" (`default` recommended)
- "Which runtime mode should I set?" (`gateway` recommended, `native-cleanup` only for cleanup transition, `native-direct` for direct Claude scope)
- "Which model should I set?" (`gpt-5.3-codex` recommended, `codex-spark` available; for `vendor=claude`: `claude-sonnet-4-6` recommended, `claude-opus-4-6` for max intelligence)
- "Should I isolate settings per project (`project`) or apply globally (`user`)?" (`project` recommended)

### Execution template (agent runs all commands)

Use these commands after collecting answers:

```bash
./scripts/install_ccg.sh --source --install-dir "$HOME/.local/bin"
./scripts/verify_ccg.sh --binary "$HOME/.local/bin/ccg"

"$HOME/.local/bin/ccg" setup --vendor "$VENDOR" --profile "$PROFILE" --runtime-mode gateway --gateway-backend cliproxyapi --settings-layer "$SETTINGS_LAYER" --proxy-version latest
"$HOME/.local/bin/ccg" doctor --vendor "$VENDOR" --profile "$PROFILE"
```

If model was selected, pass it in setup:

```bash
"$HOME/.local/bin/ccg" setup --vendor "$VENDOR" --profile "$PROFILE" --runtime-mode gateway --gateway-backend cliproxyapi --settings-layer "$SETTINGS_LAYER" --proxy-version latest --model "$MODEL"
```

Interactive one-shot (recommended for guided install):

```bash
"$HOME/.local/bin/ccg" setup --interactive --vendor "$VENDOR" --profile "$PROFILE"
```

`setup` now supports `--settings-layer`/`--settings-path` directly; use bootstrap-first only for explicit pre-migration workflows:

```bash
"$HOME/.local/bin/ccg" bootstrap --vendor "$VENDOR" --profile "$PROFILE" --settings-layer project
"$HOME/.local/bin/ccg" setup --vendor "$VENDOR" --profile "$PROFILE" --runtime-mode gateway --gateway-backend cliproxyapi --proxy-version latest --model "$MODEL"
```

Cleanup transition (gateway -> native-cleanup) only:

```bash
"$HOME/.local/bin/ccg" setup --vendor "$VENDOR" --profile "$PROFILE" --runtime-mode native-cleanup
"$HOME/.local/bin/ccg" doctor --vendor "$VENDOR" --profile "$PROFILE"
```

### Policy for the agent

- Do not provide a command list and wait for the user to execute it.
- Run the setup flow on behalf of the user and report progress/results.
- If `doctor` fails, include the exact failing check and the next recovery command.
- Keep default settings isolation as `settings_layer=project`; do not switch to `user` unless the user explicitly requests global behavior.
- If `settings_layer=project|local` mismatch appears, do not continue with apply/use on the wrong cwd; run `ccg setup --vendor "$VENDOR" --profile "$PROFILE" --settings-layer "$SETTINGS_LAYER"` first.
- If `doctor` succeeds but `Past errors` still shows old failures, optionally run `ccg doctor --vendor "$VENDOR" --profile "$PROFILE" --clear-error-history`.
- Treat `Past errors` as history-only; old `ERR_INVALID_ARGS` entries (for example from prior `status --since ...` misuse) are not current failures when checks are `[OK]`.
- For post-install model changes, prefer `ccg model switch --vendor "$VENDOR" --profile "$PROFILE" --model "$MODEL"` over manual `bootstrap/service/claude` step chaining.
- `model switch` is active-scope-only. If needed, switch scope first with `ccg use --vendor "$VENDOR" --profile "$PROFILE"`.
- `model switch` enforces active snapshot/generation consistency during apply and tail verification; if active changed concurrently, switch fails with rollback.
- For `vendor=codex`, do not pass Claude selectors (`claude-*`, `opus`, `sonnet`, `haiku`) into `--model`; those are rejected by policy.
- For `vendor=claude`, supported models: `claude-sonnet-4-6` (best balance of speed/intelligence), `claude-opus-4-6` (most intelligent), `claude-haiku-4-5` (fastest).
- For intentional cross-vendor switching (e.g. switching from Codex to Claude for a different task), prefer `ccg scope switch`:
  `ccg scope switch --from codex:default --to claude:default --model claude-sonnet-4-6`
  (or `--model claude-opus-4-6` for max intelligence)
- Use `--dry-run` to validate a scope switch before executing:
  `ccg scope switch --from codex:default --to claude:default --model claude-sonnet-4-6 --dry-run`
- `scope switch` is source-active-only (like failover); the `--from` scope must be the currently active scope.
- `scope switch` uses `installtx.Transaction` for atomic rollback; on any step failure the entire operation reverts.
- `scope switch` supports same-vendor switching with different profiles (e.g. `codex:default` → `codex:work`) as an alternative to `use` + `model switch` when both scope activation and model change are needed in one step.
- `scope switch` rejects same-scope switching (`--from` and `--to` must differ); for same-scope model changes, use `ccg model switch`.
- If Codex quota/token is exhausted and user wants Claude Opus/Sonnet fallback, prefer cross-vendor failover:
  `ccg failover --from codex:default --to claude:default --model claude-sonnet-4-6`
  (or `--model claude-opus-4-6` for max intelligence)
- Before executing failover, run preflight first:
  `ccg preflight --from codex:default --to claude:default --model claude-sonnet-4-6`
- For existing failover targets, `ccg` validates target settings binding/policy before mutation; if target binding is mismatched, failover is blocked without changing target config/state.
- `failover` also validates source-active snapshot at switch time; if another command changed active scope concurrently, failover aborts and rollback avoids clobbering that external active change.
- For multi-agent/operator transfer, generate handoff bundle first:
  `ccg handoff create --from codex:default --to claude:default --model claude-sonnet-4-6 --output /tmp/ccg-handoff.md`
- If only cleanup is required (no scope switch), use:
  `ccg setup --vendor "$VENDOR" --profile "$PROFILE" --runtime-mode native-cleanup` and re-check with `ccg doctor --vendor "$VENDOR" --profile "$PROFILE"`.

## Recovery rules for known failures

If setup/start fails with one of these errors:

- `ERR_SWITCH_VALIDATION_FAILED` with `connect: connection refused`
- `ERR_LAUNCHCTL_FAILED` with `Could not find service` (macOS)
- `ERR_SYSTEMD_FAILED` with `Unit not found` or `Failed to start` (Linux)

the agent should execute:

```bash
"$HOME/.local/bin/ccg" service install --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccg" service start --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccg" doctor --vendor "$VENDOR" --profile "$PROFILE"
```

From `v0.2.4`, failed `service install` attempts automatic cleanup (macOS: bootout + plist removal; Linux: unit stop + disable + daemon-reload), so retrying the chain above is the preferred recovery path instead of manual `launchctl load` or `systemctl --user` commands.
`v0.3.0` adds `ccg service reconcile --vendor "$VENDOR" --profile "$PROFILE"` as a single recovery chain wrapper.

### Linux-specific recovery: systemd user session issues

If `service install` or `service start` fails on Linux with systemd-related errors, the agent should verify prerequisites before retrying:

```bash
# ensure user linger is enabled (required for user services without active login)
loginctl enable-linger "$(whoami)"
# ensure XDG_RUNTIME_DIR is set
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
# retry the standard recovery chain
"$HOME/.local/bin/ccg" service install --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccg" service start --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccg" doctor --vendor "$VENDOR" --profile "$PROFILE"
```

If units remain stuck after repeated failures:

```bash
systemctl --user stop "ccgateway-${VENDOR}-${PROFILE}-proxy.service" 2>/dev/null
systemctl --user disable "ccgateway-${VENDOR}-${PROFILE}-proxy.service" 2>/dev/null
systemctl --user daemon-reload
"$HOME/.local/bin/ccg" service install --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccg" service start --vendor "$VENDOR" --profile "$PROFILE"
```

If requests fail with `unknown provider for model claude-opus-4-6` (or `claude-sonnet-4-6`), the agent should treat it as stale proxy config and run:

```bash
"$HOME/.local/bin/ccg" service install --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccg" service start --vendor "$VENDOR" --profile "$PROFILE"
```

Restarting Claude Code alone is not a fix for this case.

If requests repeatedly fail with required-parameter errors (for example `query|pattern|command is missing`) and proxy transcript logs show `input: {}`, run:

```bash
"$HOME/.local/bin/ccg" auth sync --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccg" service install --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccg" service start --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccg" model switch --vendor "$VENDOR" --profile "$PROFILE" --model gpt-5.3-codex
"$HOME/.local/bin/ccg" doctor --vendor "$VENDOR" --profile "$PROFILE"
```

`ccg doctor` includes a `tool-call integrity` check for this signature.

If `model switch` fails with `proxy binary missing`, the scope was only bootstrapped (or artifact removed). Run:

```bash
"$HOME/.local/bin/ccg" setup --vendor "$VENDOR" --profile "$PROFILE" --runtime-mode gateway --gateway-backend cliproxyapi --model gpt-5.3-codex
# or minimally:
"$HOME/.local/bin/ccg" proxy install --vendor "$VENDOR" --profile "$PROFILE"
```

If strict codex policy guard blocks with `ERR_POLICY_VIOLATION`, keep scoped settings by default:

```bash
"$HOME/.local/bin/ccg" setup --vendor "$VENDOR" --profile "$PROFILE" --settings-layer project
```

If `claude revert` fails or user-level settings are tangled across scopes, run this recovery sequence:

```bash
# backup + scrub user-level managed keys
python3 - <<'PY'
import json, os, shutil, time
p=os.path.expanduser("~/.claude/settings.json")
os.makedirs(os.path.dirname(p), exist_ok=True)
if not os.path.exists(p):
    open(p, "w").write("{}\n")
bak=p+f".bak.ccg-recovery.{int(time.time())}"
shutil.copy2(p, bak)
d=json.load(open(p))
if not isinstance(d, dict):
    d={}
env=d.get("env") if isinstance(d.get("env"), dict) else {}
for k in [
  "ANTHROPIC_BASE_URL","ANTHROPIC_AUTH_TOKEN","ANTHROPIC_MODEL",
  "ANTHROPIC_SMALL_FAST_MODEL","ANTHROPIC_DEFAULT_SONNET_MODEL",
  "ANTHROPIC_DEFAULT_OPUS_MODEL","ANTHROPIC_DEFAULT_HAIKU_MODEL"
]:
    env.pop(k, None)
d.pop("model", None)
if env: d["env"]=env
elif "env" in d: d.pop("env", None)
with open(p, "w") as f:
    json.dump(d, f, indent=2); f.write("\n")
os.chmod(p, 0o600)
print("backup:", bak)
print("cleaned:", p)
PY

# rebind scopes to project-local settings
"$HOME/.local/bin/ccg" setup --vendor "$VENDOR" --profile "$PROFILE" --settings-layer project
```

If snapshot metadata is stale and revert remains blocked, last-resort reset:

```bash
python3 - <<'PY'
import json, glob, os
for sp in glob.glob(os.path.expanduser("~/.ccgateway/vendors/*/profiles/*/state.json")):
    st=json.load(open(sp))
    c=st.setdefault("claude", {})
    c["applied"]=False
    c["snapshot_path"]=""
    c["snapshot_sha256"]=""
    c["applied_generation"]=""
    with open(sp, "w") as f:
        json.dump(st, f, indent=2); f.write("\n")
    print("state-reset:", sp)
PY
```
