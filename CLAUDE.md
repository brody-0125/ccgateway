# Claude Agent Install Guide

This repository provides a deterministic install/verify flow for agents.

## Required workflow

1. Install only through `scripts/install_ccb.sh`.
2. Verify only through `scripts/verify_ccb.sh`.
3. Avoid ad-hoc `curl | bash` or manual binary replacement.
4. Do not ask users to run `bootstrap`, `proxy install`, `service`, `claude apply`, or `doctor` manually.
5. The agent must collect scope inputs first, then execute the full setup end-to-end.
6. After upgrading `ccb`, re-run `service install` for the target scope before `service start` so proxy config/plists are regenerated.
7. `setup` may auto-retry `service install -> service start` once for known launchctl/connection-refused signatures; if it still fails, use the recovery block below.

## Local development install

```bash
./scripts/install_ccb.sh --source --install-dir "$HOME/.local/bin"
./scripts/verify_ccb.sh --binary "$HOME/.local/bin/ccb"
```

`install_ccb.sh --source` defaults to the script repository root, so absolute-path invocation from another `cwd` is supported.

## GitHub Release install

```bash
./scripts/install_ccb.sh --repo <owner>/<repo> --version latest --install-dir "$HOME/.local/bin"
./scripts/verify_ccb.sh --binary "$HOME/.local/bin/ccb"
```

## Artifact checksum verification only

```bash
./scripts/verify_ccb.sh --checksums ./dist/checksums.txt --skip-binary
```

## Notes

- The project is macOS-only for install/runtime operations.
- If `/usr/local/bin` is used and not writable, `install_ccb.sh` may require `sudo`.

## Agent-first interactive setup (no manual step-by-step)

When the user asks to install/setup ccgateway, the agent should:

1. Ask configuration questions with **UserQuestion Tool** (not free-form guesswork):
   - `vendor` (recommended default: `codex`)
   - `profile` (recommended default: `default`)
   - `runtime_mode` (`gateway` recommended; `native-cleanup` is cleanup-only, `native-direct` for direct Claude scopes)
   - `model` (recommended default: `gpt-5.3-codex`; optional: `codex-spark` -> `gpt-5.3-codex-spark`)
   - `settings_layer` (recommended default: `project` for per-project/session isolation; `user` only when global routing is explicitly desired)
2. Perform install + setup commands directly.
3. Return final status/result only after `doctor` succeeds (or return actionable failure).

### Suggested UserQuestion prompts

- "Which vendor should I configure?" (`codex` recommended)
- "Which profile should I create/use?" (`default` recommended)
- "Which runtime mode should I set?" (`gateway` recommended, `native-cleanup` only for cleanup transition, `native-direct` for direct Claude scope)
- "Which model should I set?" (`gpt-5.3-codex` recommended, `codex-spark` available)
- "Should I isolate settings per project (`project`) or apply globally (`user`)?" (`project` recommended)

### Execution template (agent runs all commands)

Use these commands after collecting answers:

```bash
./scripts/install_ccb.sh --source --install-dir "$HOME/.local/bin"
./scripts/verify_ccb.sh --binary "$HOME/.local/bin/ccb"

"$HOME/.local/bin/ccb" setup --vendor "$VENDOR" --profile "$PROFILE" --runtime-mode gateway --gateway-backend cliproxyapi --settings-layer "$SETTINGS_LAYER" --proxy-version latest
"$HOME/.local/bin/ccb" doctor --vendor "$VENDOR" --profile "$PROFILE"
```

If model was selected, pass it in setup:

```bash
"$HOME/.local/bin/ccb" setup --vendor "$VENDOR" --profile "$PROFILE" --runtime-mode gateway --gateway-backend cliproxyapi --settings-layer "$SETTINGS_LAYER" --proxy-version latest --model "$MODEL"
```

Interactive one-shot (recommended for guided install):

```bash
"$HOME/.local/bin/ccb" setup --interactive --vendor "$VENDOR" --profile "$PROFILE"
```

`setup` now supports `--settings-layer`/`--settings-path` directly; use bootstrap-first only for explicit pre-migration workflows:

```bash
"$HOME/.local/bin/ccb" bootstrap --vendor "$VENDOR" --profile "$PROFILE" --settings-layer project
"$HOME/.local/bin/ccb" setup --vendor "$VENDOR" --profile "$PROFILE" --runtime-mode gateway --gateway-backend cliproxyapi --proxy-version latest --model "$MODEL"
```

Cleanup transition (gateway -> native-cleanup) only:

```bash
"$HOME/.local/bin/ccb" setup --vendor "$VENDOR" --profile "$PROFILE" --runtime-mode native-cleanup
"$HOME/.local/bin/ccb" doctor --vendor "$VENDOR" --profile "$PROFILE"
```

### Policy for the agent

- Do not provide a command list and wait for the user to execute it.
- Run the setup flow on behalf of the user and report progress/results.
- If `doctor` fails, include the exact failing check and the next recovery command.
- Keep default settings isolation as `settings_layer=project`; do not switch to `user` unless the user explicitly requests global behavior.
- If `settings_layer=project|local` mismatch appears, do not continue with apply/use on the wrong cwd; run `ccb setup --vendor "$VENDOR" --profile "$PROFILE" --settings-layer "$SETTINGS_LAYER"` first.
- If `doctor` succeeds but `Past errors` still shows old failures, optionally run `ccb doctor --vendor "$VENDOR" --profile "$PROFILE" --clear-error-history`.
- Treat `Past errors` as history-only; old `ERR_INVALID_ARGS` entries (for example from prior `status --since ...` misuse) are not current failures when checks are `[OK]`.
- For post-install model changes, prefer `ccb model switch --vendor "$VENDOR" --profile "$PROFILE" --model "$MODEL"` over manual `bootstrap/service/claude` step chaining.
- `model switch` is active-scope-only. If needed, switch scope first with `ccb use --vendor "$VENDOR" --profile "$PROFILE"`.
- `model switch` enforces active snapshot/generation consistency during apply and tail verification; if active changed concurrently, switch fails with rollback.
- For `vendor=codex`, do not pass Claude selectors (`claude-*`, `opus`, `sonnet`, `haiku`) into `--model`; those are rejected by policy.
- For intentional cross-vendor switching (e.g. switching from Codex to Claude for a different task), prefer `ccb scope switch`:
  `ccb scope switch --from codex:default --to claude:default --model claude-opus-4-6`
- Use `--dry-run` to validate a scope switch before executing:
  `ccb scope switch --from codex:default --to claude:default --model claude-opus-4-6 --dry-run`
- `scope switch` is source-active-only (like failover); the `--from` scope must be the currently active scope.
- `scope switch` uses `installtx.Transaction` for atomic rollback; on any step failure the entire operation reverts.
- `scope switch` also supports same-vendor switching (different profiles) as an alternative to `use` + `model switch` when both scope activation and model change are needed in one step.
- If Codex quota/token is exhausted and user wants Claude Opus/Sonnet fallback, prefer cross-vendor failover:
  `ccb failover --from codex:default --to claude:default --model claude-opus-4-6`
- Before executing failover, run preflight first:
  `ccb preflight --from codex:default --to claude:default --model claude-opus-4-6`
- For existing failover targets, `ccb` validates target settings binding/policy before mutation; if target binding is mismatched, failover is blocked without changing target config/state.
- `failover` also validates source-active snapshot at switch time; if another command changed active scope concurrently, failover aborts and rollback avoids clobbering that external active change.
- For multi-agent/operator transfer, generate handoff bundle first:
  `ccb handoff create --from codex:default --to claude:default --model claude-opus-4-6 --output /tmp/ccb-handoff.md`
- If only cleanup is required (no scope switch), use:
  `ccb setup --vendor "$VENDOR" --profile "$PROFILE" --runtime-mode native-cleanup` and re-check with `ccb doctor --vendor "$VENDOR" --profile "$PROFILE"`.

## Recovery rules for known failures

If setup/start fails with one of these errors:

- `ERR_SWITCH_VALIDATION_FAILED` with `connect: connection refused`
- `ERR_LAUNCHCTL_FAILED` with `Could not find service`

the agent should execute:

```bash
"$HOME/.local/bin/ccb" service install --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccb" service start --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccb" doctor --vendor "$VENDOR" --profile "$PROFILE"
```

From `v0.2.4`, failed `service install` attempts launchd cleanup automatically (bootout + plist removal), so retrying the chain above is the preferred recovery path instead of manual `launchctl load`.
`v0.3.0` adds `ccb service reconcile --vendor "$VENDOR" --profile "$PROFILE"` as a single recovery chain wrapper.

If requests fail with `unknown provider for model claude-opus-4-6`, the agent should treat it as stale proxy config and run:

```bash
"$HOME/.local/bin/ccb" service install --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccb" service start --vendor "$VENDOR" --profile "$PROFILE"
```

Restarting Claude Code alone is not a fix for this case.

If requests repeatedly fail with required-parameter errors (for example `query|pattern|command is missing`) and proxy transcript logs show `input: {}`, run:

```bash
"$HOME/.local/bin/ccb" auth sync --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccb" service install --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccb" service start --vendor "$VENDOR" --profile "$PROFILE"
"$HOME/.local/bin/ccb" model switch --vendor "$VENDOR" --profile "$PROFILE" --model gpt-5.3-codex
"$HOME/.local/bin/ccb" doctor --vendor "$VENDOR" --profile "$PROFILE"
```

`ccb doctor` includes a `tool-call integrity` check for this signature.

If `model switch` fails with `proxy binary missing`, the scope was only bootstrapped (or artifact removed). Run:

```bash
"$HOME/.local/bin/ccb" setup --vendor "$VENDOR" --profile "$PROFILE" --runtime-mode gateway --gateway-backend cliproxyapi --model gpt-5.3-codex
# or minimally:
"$HOME/.local/bin/ccb" proxy install --vendor "$VENDOR" --profile "$PROFILE"
```

If strict codex policy guard blocks with `ERR_POLICY_VIOLATION`, keep scoped settings by default:

```bash
"$HOME/.local/bin/ccb" setup --vendor "$VENDOR" --profile "$PROFILE" --settings-layer project
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
bak=p+f".bak.ccb-recovery.{int(time.time())}"
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
"$HOME/.local/bin/ccb" setup --vendor "$VENDOR" --profile "$PROFILE" --settings-layer project
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
