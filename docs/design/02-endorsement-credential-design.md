# Endorsement Credential - Design

> Status: Draft
> Date: 2026-02-21
> Author: ccgateway team
> Depends on: [Research - Endorsement Credential](../research/02-endorsement-credential.md)

## 1. Overview

This document specifies the detailed design for adding HMAC-based endorsement
credentials to ccgateway. The endorsement credential replaces the current
static `ccg::<vendor>::<profile>::<generation>` token with a time-bound,
cryptographically signed credential that proves a request was authorized by
ccgateway for a specific scope.

### 1.1 Goals

1. Issue short-lived, HMAC-signed credentials at `claude apply` time
2. Enable proxy-side verification of credential authenticity and freshness
3. Maintain backwards compatibility with existing `ccg::` and `proxy-local` tokens
4. Integrate into the existing scope lifecycle without breaking any commands
5. Add doctor checks for credential health (expiry, secret existence)

### 1.2 Non-Goals

- Replacing upstream auth (Codex OAuth tokens)
- Adding mTLS or asymmetric cryptography
- Per-request nonce tracking (Phase 1)
- Automatic background credential renewal (Phase 1)

## 2. Architecture

### 2.1 Component Diagram

```
                        ccg CLI
                           |
                  [endorsement.Issue()]
                           |
                     +-----------+
                     | secret.key|  (per-scope, 32 bytes)
                     +-----------+
                           |
            +--------------+--------------+
            |                             |
    Claude settings.json           Proxy config.yaml
    (ANTHROPIC_AUTH_TOKEN)         (endorsement_secret_path)
            |                             |
            v                             v
       Claude Code  ---HTTP--->  ccgateway proxy
                                [endorsement.Verify()]
```

### 2.2 New Package: `internal/endorsement`

A single new package encapsulates all credential logic:

```
internal/endorsement/
  endorsement.go      -- Core types, Issue(), Verify(), secret management
  endorsement_test.go -- Unit tests
```

## 3. Detailed Design

### 3.1 Credential Format

```
ccge::1::<base64url-encoded-payload>
```

Where:
- `ccge::` -- prefix identifying endorsement credentials (vs legacy `ccg::`)
- `1` -- credential format version
- Base64url-encoded payload contains:

```
+------------------+------------------+------------------+------------------+
| scope_id (var)   | issued_at (8B)   | nonce (16B)      | hmac (32B)       |
+------------------+------------------+------------------+------------------+
```

#### Wire format (binary, before base64url encoding):

| Field | Size | Description |
|-------|------|-------------|
| `scope_id_len` | 1 byte | Length of scope_id string |
| `scope_id` | variable (max 255) | `"<vendor>:<profile>"` UTF-8 |
| `issued_at` | 8 bytes | Unix timestamp (seconds), big-endian uint64 |
| `nonce` | 16 bytes | `crypto/rand` random bytes |
| `hmac` | 32 bytes | HMAC-SHA256 over all preceding fields |

#### Example token:

```
ccge::1::DmNvZGV4OmRlZmF1bHQAAAAAZxjk8K3F2...
```

Total base64-encoded size: ~100-110 characters (fits comfortably in env var).

### 3.2 Core Types

```go
package endorsement

import (
    "crypto/hmac"
    "crypto/rand"
    "crypto/sha256"
    "encoding/base64"
    "encoding/binary"
    "fmt"
    "os"
    "strings"
    "time"
)

const (
    Prefix       = "ccge::"
    Version      = "1"
    SecretSize   = 32   // bytes
    NonceSize    = 16   // bytes
    HMACSize     = 32   // SHA-256 output
    DefaultTTL   = 3600 // seconds (1 hour)

    // File name for per-scope secret key
    SecretFileName = "endorsement.key"
)

// Credential represents a parsed endorsement credential.
type Credential struct {
    ScopeID  string
    IssuedAt time.Time
    Nonce    []byte
    HMAC     []byte
}

// IssueOptions configures credential issuance.
type IssueOptions struct {
    ScopeID    string // "vendor:profile"
    SecretPath string // path to 32-byte secret key file
    TTL        int    // seconds; 0 means DefaultTTL
}

// VerifyOptions configures credential verification.
type VerifyOptions struct {
    SecretPath    string // path to 32-byte secret key file
    ExpectedScope string // expected "vendor:profile"; empty = skip scope check
    TTL           int    // seconds; 0 means DefaultTTL
    Now           func() time.Time // for testing; nil = time.Now
}
```

### 3.3 Secret Key Management

```go
// EnsureSecret creates a new random secret key at path if one does not exist.
// Returns the secret bytes (always 32 bytes).
func EnsureSecret(path string) ([]byte, error)

// LoadSecret reads the secret key from path.
func LoadSecret(path string) ([]byte, error)

// RotateSecret replaces the secret key at path with a new random key.
// Returns the new secret bytes.
func RotateSecret(path string) ([]byte, error)
```

**Storage location:** `~/.ccgateway/vendors/<v>/profiles/<p>/endorsement/endorsement.key`

**File permissions:** `0600` (owner-read/write only)

**Scope paths extension** (`internal/scope/scope.go`):

```go
type Paths struct {
    // ... existing fields ...
    EndorsementDir    string
    EndorsementSecret string
}
```

### 3.4 Issuance

```go
// Issue creates a new endorsement credential string.
func Issue(opts IssueOptions) (string, error) {
    // 1. Load secret from file
    // 2. Build binary payload: scope_id_len + scope_id + issued_at + nonce
    // 3. Compute HMAC-SHA256(secret, payload)
    // 4. Append HMAC to payload
    // 5. Return "ccge::1::" + base64url(payload + hmac)
}
```

### 3.5 Verification

```go
// Verify parses and verifies an endorsement credential string.
// Returns the parsed Credential on success, or an error describing the failure.
func Verify(token string, opts VerifyOptions) (*Credential, error) {
    // 1. Check "ccge::" prefix
    // 2. Check version == "1"
    // 3. Base64url-decode payload
    // 4. Split: payload fields + trailing 32-byte HMAC
    // 5. Load secret from file
    // 6. Recompute HMAC over payload fields
    // 7. Constant-time compare with presented HMAC
    // 8. Check issued_at + TTL >= now
    // 9. Optionally check scope_id matches expected
    // 10. Return parsed Credential
}
```

### 3.6 Legacy Token Support

```go
// IsManagedToken checks if a token is any ccgateway-managed format.
// Supports both legacy (ccg::, proxy-local) and endorsement (ccge::) tokens.
func IsManagedToken(token string) bool {
    return IsEndorsement(token) || isLegacyManaged(token)
}

// IsEndorsement checks if a token is an endorsement credential.
func IsEndorsement(token string) bool {
    return strings.HasPrefix(token, Prefix)
}
```

## 4. Integration Points

### 4.1 Scope Paths (`internal/scope/scope.go`)

Add endorsement paths to `BuildPaths()`:

```go
EndorsementDir:    filepath.Join(scopeDir, "endorsement"),
EndorsementSecret: filepath.Join(scopeDir, "endorsement", "endorsement.key"),
```

### 4.2 Config (`internal/config/config.go`)

Add endorsement mode to Config struct:

```go
type Config struct {
    // ... existing fields ...
    EndorsementMode string // "disabled" | "enabled" | "strict"
}
```

Default values:
- New scopes: `"enabled"` (issue endorsement credentials)
- Existing scopes without the field: treated as `"disabled"` (backwards compatibility)

Config file key: `endorsement_mode`

### 4.3 State (`internal/state/state.go`)

Add endorsement state tracking:

```go
type EndorsementState struct {
    SecretSHA256 string `json:"secret_sha256,omitempty"`
    LastIssued   string `json:"last_issued,omitempty"`   // RFC3339
    TTL          int    `json:"ttl,omitempty"`           // seconds
}

type State struct {
    // ... existing fields ...
    Endorsement EndorsementState `json:"endorsement,omitempty"`
}
```

### 4.4 Provider - Codex (`internal/provider/codex/codex.go`)

Modify `claudePatcher.Apply()` to issue endorsement credentials:

```go
func (claudePatcher) Apply(_ context.Context, rt provider.ScopeRuntime, generation string) (claude.ApplyResult, error) {
    mode := claude.ApplyModeGateway
    var token string

    if rt.Config.EndorsementMode != "disabled" && rt.Config.EndorsementMode != "" {
        // Issue endorsement credential
        cred, err := endorsement.Issue(endorsement.IssueOptions{
            ScopeID:    rt.Ref.ScopeID(),
            SecretPath: rt.Paths.EndorsementSecret,
        })
        if err != nil {
            return claude.ApplyResult{}, fmt.Errorf("endorsement issue: %w", err)
        }
        token = cred
    } else {
        // Legacy token
        token = fmt.Sprintf("ccg::%s::%s::%s", rt.Ref.VendorID, rt.Ref.ProfileID, generation)
    }

    if rt.Config.RuntimeMode == config.RuntimeModeNativeCleanup || !rt.Config.ProxyEnabled {
        mode = claude.ApplyModeNativeCleanup
        token = ""
    }

    return claude.ApplyWithOptions(claude.ApplyOptions{
        SettingsPath: rt.Config.SettingsPath,
        SnapshotDir:  rt.Paths.SnapshotsDir,
        Port:         rt.Config.Port,
        Model:        rt.Config.Model,
        AuthToken:    token,
        Mode:         mode,
    })
}
```

### 4.5 Claude Settings (`internal/claude/settings.go`)

Update `isManagedProxyToken()` to recognize endorsement tokens:

```go
func isManagedProxyToken(v string) bool {
    s := strings.TrimSpace(v)
    return s == "proxy-local" ||
        strings.HasPrefix(s, "ccg::") ||
        strings.HasPrefix(s, "ccge::")
}
```

### 4.6 Proxy Config (`internal/proxy/config.go`)

Add endorsement secret path to proxy configuration:

```go
func WriteProxyConfig(path string, port int, authDir, model, endorsementSecretPath string) error {
    // ... existing config ...
    if endorsementSecretPath != "" {
        body += fmt.Sprintf("endorsement-secret: %q\n", endorsementSecretPath)
    }
    // ...
}
```

The proxy binary reads this path and uses it to verify incoming credentials.

### 4.7 Doctor Checks (`internal/doctor/checks.go`)

Add new checks:

```go
// endorsementSecretCheck verifies the secret key file exists and has correct permissions.
func endorsementSecretCheck(input ScopedInput) CheckResult

// endorsementFreshnessCheck verifies the credential in settings is not expired.
func endorsementFreshnessCheck(input ScopedInput) CheckResult
```

**endorsementSecretCheck:**
- Verify `endorsement.key` exists at scope path
- Verify file permissions are `0600`
- Verify file is owned by current user
- Verify file size is exactly 32 bytes

**endorsementFreshnessCheck:**
- Read `ANTHROPIC_AUTH_TOKEN` from settings
- If it's a `ccge::` token, decode and check expiry
- Warn if expired or within 10 minutes of expiry

### 4.8 Route Proof Check Update

Update `routeProofCheck()` in doctor to handle both token formats:

```go
// In routeProofCheck for gateway mode:
if endorsement.IsEndorsement(gotToken) {
    // Verify credential is valid and matches scope
    cred, err := endorsement.Verify(gotToken, endorsement.VerifyOptions{
        SecretPath:    input.Paths.EndorsementSecret,
        ExpectedScope: input.Scope.ScopeID(),
    })
    if err != nil {
        return CheckResult{Name: "route proof", OK: false, Detail: "endorsement credential invalid: " + err.Error()}
    }
    // Valid endorsement
} else if strings.HasPrefix(gotToken, wantTokenPrefix) || gotToken == "proxy-local" {
    // Legacy token (acceptable during migration)
} else {
    return CheckResult{Name: "route proof", OK: false, Detail: "unrecognized auth token format"}
}
```

### 4.9 Setup Flow (`internal/cli/commands.go`)

In `cmdSetup()`, add endorsement secret provisioning before `claude apply`:

```go
// After service install, before claude apply:
if mode == config.RuntimeModeGateway && endorsementMode != "disabled" {
    if _, err := endorsement.EnsureSecret(paths.EndorsementSecret); err != nil {
        return wrapStepErr("endorsement secret", err)
    }
}
```

### 4.10 Uninstall (`internal/cli/commands.go`)

In `cmdUninstall()`, clean up endorsement secret:

```go
// During purge:
_ = os.RemoveAll(paths.EndorsementDir)
```

## 5. New CLI Commands

### 5.1 `ccg endorsement status`

Display endorsement credential status for a scope:

```
$ ccg endorsement status --vendor codex --profile default
endorsement_mode: enabled
secret: present (sha256: a1b2c3...)
credential: valid (expires in 47m)
scope: codex:default
```

### 5.2 `ccg endorsement rotate`

Rotate the endorsement secret and re-issue credential:

```
$ ccg endorsement rotate --vendor codex --profile default
secret rotated (sha256: d4e5f6...)
credential re-issued (expires in 1h)
claude settings updated
```

This command:
1. Generates new secret key
2. Issues new credential with new secret
3. Updates Claude settings
4. Updates proxy config (if secret path changed)
5. Restarts proxy service to pick up new secret

## 6. Error Codes

Add new error codes to `internal/errors/codes.go`:

```go
const (
    ErrEndorsementIssueFailed  = "ERR_ENDORSEMENT_ISSUE_FAILED"
    ErrEndorsementVerifyFailed = "ERR_ENDORSEMENT_VERIFY_FAILED"
    ErrEndorsementExpired      = "ERR_ENDORSEMENT_EXPIRED"
    ErrEndorsementSecretMissing = "ERR_ENDORSEMENT_SECRET_MISSING"
)
```

## 7. Configuration Schema Changes

### 7.1 Scope Config (`config.yaml`)

```yaml
# ... existing fields ...
endorsement_mode: "enabled"    # disabled | enabled | strict
endorsement_ttl: 3600          # seconds, default 3600
```

### 7.2 Proxy Config (generated)

```yaml
port: 8317
auth-dir: "/path/to/auth"
endorsement-secret: "/path/to/endorsement.key"
# ... existing fields ...
```

### 7.3 State (`state.json`)

```json
{
  "endorsement": {
    "secret_sha256": "a1b2c3...",
    "last_issued": "2026-02-21T12:00:00Z",
    "ttl": 3600
  }
}
```

## 8. Migration Plan

### 8.1 Phase 1: Dual-Mode Support (This Implementation)

**Default for new scopes:** `endorsement_mode: enabled`
**Default for existing scopes:** `endorsement_mode: disabled` (preserves behavior)

When `endorsement_mode: enabled`:
- `ccg setup` creates endorsement secret
- `ccg claude apply` issues `ccge::` credential
- Proxy accepts both `ccge::` and `ccg::` tokens
- Doctor reports endorsement status

When `endorsement_mode: disabled`:
- All current behavior unchanged
- No secret created, no endorsement issuance

### 8.2 Phase 2: Deprecation Warnings (Future)

- Doctor warns when `endorsement_mode: disabled`
- Doctor warns when legacy `ccg::` token detected in settings
- `ccg setup` prompts to enable endorsement if disabled

### 8.3 Phase 3: Strict Enforcement (Future)

- `endorsement_mode: strict` becomes recommended default
- Proxy rejects legacy tokens
- `ccg setup` requires endorsement (no `disabled` option)

### 8.4 Upgrade Path

```bash
# Existing scope: enable endorsement
ccg bootstrap --vendor codex --profile default --endorsement-mode enabled
ccg service install --vendor codex --profile default
ccg service start --vendor codex --profile default
ccg claude apply --vendor codex --profile default
ccg doctor --vendor codex --profile default
```

Or one-shot:
```bash
ccg setup --vendor codex --profile default --endorsement-mode enabled
```

## 9. Testing Strategy

### 9.1 Unit Tests (`internal/endorsement/endorsement_test.go`)

| Test | Description |
|------|-------------|
| `TestIssueAndVerify` | Round-trip: issue credential, verify it succeeds |
| `TestVerifyExpired` | Issue with short TTL, wait, verify returns expiry error |
| `TestVerifyWrongSecret` | Verify with different secret fails |
| `TestVerifyWrongScope` | Verify with mismatched expected scope fails |
| `TestVerifyTampered` | Modify credential bytes, verify fails |
| `TestVerifyLegacyToken` | Legacy token returns appropriate error (not endorsement) |
| `TestEnsureSecret` | Creates secret file with correct size and permissions |
| `TestEnsureSecretIdempotent` | Repeated calls don't overwrite existing secret |
| `TestRotateSecret` | New secret is different from old |
| `TestIsManagedToken` | Recognizes all managed token formats |
| `TestCredentialFormat` | Verifies `ccge::1::` prefix and base64url payload |

### 9.2 Integration Tests (CLI)

| Test | Description |
|------|-------------|
| `TestSetupWithEndorsement` | Full setup flow creates secret and issues credential |
| `TestClaudeApplyEndorsement` | Apply writes endorsement token to settings |
| `TestDoctorEndorsementChecks` | Doctor validates secret and credential freshness |
| `TestModelSwitchReissue` | Model switch re-issues credential |
| `TestFailoverEndorsement` | Failover creates secret for target scope |
| `TestUninstallCleansSecret` | Purge removes endorsement directory |
| `TestMigrationLegacyToEndorsement` | Upgrade existing scope to endorsement mode |
| `TestEndorsementRotate` | Rotate command replaces secret and credential |

### 9.3 Backwards Compatibility Tests

| Test | Description |
|------|-------------|
| `TestDisabledModePreservesLegacy` | `endorsement_mode: disabled` uses `ccg::` token |
| `TestProxyAcceptsBothFormats` | Doctor route proof accepts both token formats |
| `TestRevertEndorsementToken` | SmartRevert handles endorsement tokens correctly |

## 10. Implementation Task Breakdown

### Phase 1: Core Package (Priority: P0)

| # | Task | Package | Estimated effort |
|---|------|---------|-----------------|
| 1.1 | Create `internal/endorsement/endorsement.go` with types, `Issue()`, `Verify()` | endorsement | S |
| 1.2 | Implement `EnsureSecret()`, `LoadSecret()`, `RotateSecret()` | endorsement | S |
| 1.3 | Implement `IsManagedToken()`, `IsEndorsement()` helpers | endorsement | XS |
| 1.4 | Write unit tests | endorsement | S |

### Phase 2: Scope & Config Integration (Priority: P0)

| # | Task | Package | Estimated effort |
|---|------|---------|-----------------|
| 2.1 | Add `EndorsementDir`, `EndorsementSecret` to `scope.Paths` | scope | XS |
| 2.2 | Add `EndorsementMode`, `EndorsementTTL` to `config.Config` | config | XS |
| 2.3 | Add `EndorsementState` to `state.State` | state | XS |
| 2.4 | Add error codes to `errors/codes.go` | errors | XS |

### Phase 3: Provider & Claude Integration (Priority: P0)

| # | Task | Package | Estimated effort |
|---|------|---------|-----------------|
| 3.1 | Update codex provider `Apply()` to use endorsement when enabled | provider/codex | S |
| 3.2 | Update `isManagedProxyToken()` to recognize `ccge::` | claude | XS |
| 3.3 | Update `WriteProxyConfig()` with endorsement secret path | proxy | XS |

### Phase 4: CLI Integration (Priority: P0)

| # | Task | Package | Estimated effort |
|---|------|---------|-----------------|
| 4.1 | Add `--endorsement-mode` flag to `setup` and `bootstrap` | cli | S |
| 4.2 | Wire endorsement secret creation into setup flow | cli | S |
| 4.3 | Wire endorsement secret into service install (proxy config) | cli | S |
| 4.4 | Add `endorsement` subcommand (`status`, `rotate`) | cli | M |
| 4.5 | Update uninstall purge to clean endorsement dir | cli | XS |

### Phase 5: Doctor & Status (Priority: P1)

| # | Task | Package | Estimated effort |
|---|------|---------|-----------------|
| 5.1 | Add `endorsementSecretCheck` to doctor | doctor | S |
| 5.2 | Add `endorsementFreshnessCheck` to doctor | doctor | S |
| 5.3 | Update `routeProofCheck` for dual-format tokens | doctor | S |
| 5.4 | Update `ccg status --json` with endorsement info | cli | S |

### Phase 6: Tests (Priority: P0)

| # | Task | Package | Estimated effort |
|---|------|---------|-----------------|
| 6.1 | Unit tests for `internal/endorsement` | endorsement | S |
| 6.2 | CLI integration tests for endorsement flows | cli | M |
| 6.3 | Backwards compatibility tests | cli | S |

**Size legend:** XS = <30 min, S = 30min-2h, M = 2h-4h

### Dependency Graph

```
Phase 1 (core package)
    |
    v
Phase 2 (scope/config/state)
    |
    +------+------+
    |             |
    v             v
Phase 3         Phase 4
(providers)     (CLI)
    |             |
    +------+------+
           |
           v
         Phase 5
         (doctor)
           |
           v
         Phase 6
         (tests - spans all phases)
```

## 11. Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Proxy does not support endorsement verification | High (external binary) | Blocks strict mode | Phase 1 is ccg-side only; proxy verification is future work via proxy update |
| Secret file permission issues on some platforms | Low | Credential issuance fails | Robust `EnsureSecret()` with explicit `os.Chmod()` after write |
| TTL too short causes user disruption | Medium | Repeated re-apply needed | Default 1h TTL; configurable; doctor warns before expiry |
| Config migration breaks existing scopes | Low | Scope becomes unusable | Default `disabled` for existing; explicit opt-in only |

## 12. Future Considerations

- **Automatic renewal daemon**: Extend sync agent to refresh credentials alongside auth tokens
- **Per-request nonce tracking**: Add nonce dedup cache in proxy for replay prevention
- **Audit logging**: Log credential issuance/verification events for compliance
- **Key derivation**: Derive per-session keys from master scope secret for forward secrecy
- **Cross-scope delegation**: Allow one scope's credential to be endorsed by another (multi-project)
