# Endorsement Credential - Research

> Status: Draft
> Date: 2026-02-21
> Author: ccgateway team

## 1. Problem Statement

ccgateway currently uses a static `ANTHROPIC_AUTH_TOKEN` injected into Claude
settings at `claude apply` time. This token is either the literal string
`"proxy-local"` or a scope-stamped token of the form
`ccg::<vendor>::<profile>::<generation>`.

These tokens serve only as **routing markers** -- they identify which scope
generated the settings -- but carry no cryptographic proof that the caller
actually transited the ccgateway proxy. Any process that can read or replicate
the token format can impersonate a valid scope.

**Core gaps:**

| Gap | Current state | Desired state |
|-----|---------------|---------------|
| Authenticity | Token is a static string template; no proof of transit | Short-lived credential that proves the request passed through ccgateway proxy |
| Freshness | Token has infinite lifetime once written to settings | Token expires; requires periodic renewal |
| Scope binding | Token contains scope IDs as plain text | Scope identity is cryptographically bound to the credential |
| Tamper detection | None; token can be copied or fabricated | Credential signature detects modification |
| Revocation | None; only manual settings revert removes the token | Credential can be revoked per-scope or globally |

## 2. Existing Auth Flow Analysis

### 2.1 Auth Source to Proxy (Upstream Auth)

The external auth (Codex OAuth tokens from `~/.codex/auth.json`) is synced to
a bridge format at scope-level via `internal/auth/sync.go`:

```
~/.codex/auth.json  -->  auth.Sync()  -->  ~/.ccgateway/vendors/<v>/profiles/<p>/auth/codex-from-codex-cli.json
```

The proxy (`cli-proxy-api`) reads the bridge auth file for upstream API calls.
A periodic sync agent (launchd/systemd timer) keeps this bridge file fresh.

**Key observation:** This flow handles **upstream authentication** (proxy-to-API).
The endorsement credential addresses a different concern: **downstream
authentication** (Claude-Code-to-proxy).

### 2.2 Claude Settings Token (Downstream Auth)

At `claude apply` time (`internal/provider/codex/codex.go:33-35`):

```go
token := fmt.Sprintf("ccg::%s::%s::%s", rt.Ref.VendorID, rt.Ref.ProfileID, generation)
```

This token is written into `.claude/settings.json` as
`env.ANTHROPIC_AUTH_TOKEN`. Claude Code sends it on every API request to the
local proxy (`ANTHROPIC_BASE_URL = http://127.0.0.1:<port>`).

The proxy currently does **no validation** of this token beyond checking that
the request arrives on the expected port. The token exists primarily for:
- Doctor's `route proof` check (verifying settings point to the right scope)
- `isManagedProxyToken()` checks during revert/cleanup

### 2.3 Active Generation

`control.NextGeneration()` produces `gen-<unix-nano>` strings. These are
stored in both `active.json` and `state.json` to detect concurrent scope
switches. The generation is embedded in the auth token but never
cryptographically verified.

### 2.4 Trust Boundary Map

```
 Claude Code
    |
    | HTTP (localhost) with ANTHROPIC_AUTH_TOKEN
    v
 ccgateway proxy (127.0.0.1:<port>)
    |
    | upstream auth (bridge auth file, OAuth tokens)
    v
 Codex/Claude API (remote)
```

The endorsement credential lives on the **first hop**: Claude Code to
ccgateway proxy.

## 3. Threat Model

### 3.1 In Scope

| Threat | Severity | Mitigation via endorsement |
|--------|----------|---------------------------|
| Token replay by co-tenant process | Medium | Short TTL + nonce prevents reuse |
| Token fabrication | Medium | HMAC/signature over scope+timestamp binds to proxy secret |
| Stale scope detection | Low | Expiry forces re-endorsement after scope switch |
| Settings file copy attack | Medium | Time-bound credential invalidates copied settings |

### 3.2 Out of Scope

| Threat | Reason |
|--------|--------|
| Kernel-level memory inspection | Endorsement assumes localhost channel integrity |
| Proxy binary tampering | Covered by proxy artifact checksum verification |
| Upstream API credential theft | Separate concern; auth sync handles upstream tokens |
| Network MitM on localhost | Localhost HTTP is standard; mTLS on localhost adds complexity without proportionate benefit |

## 4. Industry Approaches

### 4.1 HMAC-Based Bearer Token (Selected Approach)

A lightweight credential scheme where the proxy holds a secret key and issues
time-bound HMAC tokens:

```
credential = base64(scope_id || timestamp || nonce || HMAC-SHA256(secret, scope_id || timestamp || nonce))
```

**Advantages:**
- No external key infrastructure needed
- Stateless verification (no database lookup)
- Compatible with existing `ANTHROPIC_AUTH_TOKEN` transport
- Sub-millisecond issuance and verification

**Tradeoffs:**
- Symmetric key: both issuer (proxy/ccg) and verifier (proxy) share the same secret
- Secret rotation requires credential re-issuance

### 4.2 Alternatives Considered

| Approach | Why not |
|----------|---------|
| JWT with RSA/ECDSA | Overkill for localhost; asymmetric signing adds latency and key management complexity |
| mTLS client certificates | Requires CA setup, certificate lifecycle management; disproportionate for localhost proxy |
| OAuth 2.0 local flow | Adds OAuth server dependency; unsuitable for single-machine CLI tool |
| Simple nonce + timestamp | No scope binding; weaker than HMAC |

### 4.3 Token Format Comparison

| Format | Auth payload | Verification | Key management | Expiry |
|--------|-------------|--------------|----------------|--------|
| Current `ccg::` token | ~60 bytes | String prefix match | None | Infinite |
| HMAC endorsement | ~120 bytes | HMAC-SHA256 recompute | 32-byte secret per scope | Configurable TTL |
| JWT (HS256) | ~300 bytes | Header+payload parse + HMAC | 32-byte secret | `exp` claim |
| JWT (ES256) | ~400 bytes | Signature verification | Key pair + optional CA | `exp` claim |

## 5. Current Codebase Integration Points

### 5.1 Token Issuance Sites

| Location | Code | Role |
|----------|------|------|
| `internal/provider/codex/codex.go:35` | `token := fmt.Sprintf("ccg::%s::%s::%s", ...)` | Generates current static token |
| `internal/claude/settings.go:99` | `env["ANTHROPIC_AUTH_TOKEN"] = authToken` | Writes token to Claude settings |

### 5.2 Token Verification Sites

| Location | Code | Role |
|----------|------|------|
| `internal/claude/settings.go:390-393` | `isManagedProxyToken()` | Checks `"proxy-local"` or `"ccg::"` prefix |
| `internal/doctor/checks.go:461` | `wantTokenPrefix` in `routeProofCheck` | Verifies token format in doctor |
| `internal/doctor/checks.go:480` | Token prefix comparison | Route proof validation |

### 5.3 Secret Storage

Currently no secret storage exists. The endorsement credential requires:
- A per-scope secret key stored at `~/.ccgateway/vendors/<v>/profiles/<p>/endorsement/secret.key`
- Secret lifecycle managed alongside service install/start

### 5.4 Affected Commands

| Command | Impact |
|---------|--------|
| `ccg setup` | Must generate endorsement secret during scope setup |
| `ccg claude apply` | Must issue endorsement credential instead of static token |
| `ccg service install` | Must provision secret before proxy starts |
| `ccg service start` | Proxy must load secret for request verification |
| `ccg model switch` | Must re-issue credential with new generation |
| `ccg failover` | Must issue credential for target scope |
| `ccg scope switch` | Must issue credential for target scope |
| `ccg doctor` | Must validate credential freshness and secret existence |
| `ccg claude revert` | Must handle reverting endorsement-managed tokens |
| `ccg uninstall` | Must clean up secret material |

## 6. Credential Lifecycle

### 6.1 Issuance

```
ccg setup / ccg claude apply
  1. Generate 32-byte random secret (if not exists) -> store at scope endorsement path
  2. Build credential payload: scope_id + unix_timestamp + random_nonce(16 bytes)
  3. Compute HMAC-SHA256(secret, payload)
  4. Encode: "ccge::" + base64url(payload || hmac)
  5. Write to settings as ANTHROPIC_AUTH_TOKEN
```

### 6.2 Verification (Proxy-Side)

```
On each incoming request:
  1. Extract ANTHROPIC_AUTH_TOKEN from request header
  2. If not "ccge::" prefix, reject (or fall back to legacy check during migration)
  3. Decode base64url, split payload and HMAC
  4. Recompute HMAC with stored secret
  5. Compare (constant-time) with presented HMAC
  6. Check timestamp + TTL against current time
  7. Accept or reject
```

### 6.3 Renewal

Credentials expire after TTL (default: 1 hour). Renewal options:
- **Lazy renewal**: `ccg doctor` / `ccg status` detects near-expiry and suggests re-apply
- **Proactive renewal**: sync timer agent re-issues credential alongside auth sync
- **On-demand**: proxy returns 401 with `X-CCG-Credential-Expired` header; Claude Code restarts trigger re-apply

### 6.4 Revocation

- **Scope-level**: Delete secret key file; all credentials for that scope become invalid
- **Global**: Delete all secret key files under `~/.ccgateway/`
- `ccg uninstall --purge` removes secrets as part of full cleanup

## 7. Migration Strategy

### 7.1 Backwards Compatibility

The proxy must support both token formats during migration:
1. Legacy `proxy-local` and `ccg::` tokens (existing behavior)
2. New `ccge::` endorsement credentials

### 7.2 Migration Phases

| Phase | Scope | Behavior |
|-------|-------|----------|
| Phase 0 (current) | All scopes | Static `ccg::` tokens, no verification |
| Phase 1 | New `setup`/`apply` | Issue `ccge::` credentials; proxy accepts both formats |
| Phase 2 | All scopes | Proxy warns on legacy token format in doctor |
| Phase 3 | Future | Proxy rejects legacy tokens; strict endorsement only |

### 7.3 Config Extension

The scope config already has `auth_mode` field (`oauth_file`). Add a separate
field to avoid conflating upstream and downstream auth:

```
endorsement_mode: disabled | enabled | strict
```

- `disabled`: current behavior (static `ccg::` token)
- `enabled`: issue endorsement credentials; accept both formats (Phase 1-2)
- `strict`: reject non-endorsement tokens (Phase 3)

## 8. Performance Considerations

| Operation | Expected latency |
|-----------|-----------------|
| HMAC-SHA256 compute | < 1 microsecond |
| Secret file read (cached) | < 100 microseconds (OS page cache) |
| Base64 encode/decode | < 1 microsecond |
| Total overhead per request | < 0.2 milliseconds |

The credential overhead is negligible compared to the upstream API call latency
(typically 100ms-10s).

## 9. Open Questions

1. **TTL granularity**: Should TTL be configurable per scope or global?
   Recommendation: global default with per-scope override.

2. **Proxy secret delivery**: Should the secret be passed via file path in proxy config or via environment variable?
   Recommendation: file path in proxy config YAML.

3. **Nonce storage**: Should the proxy track seen nonces for replay prevention within TTL window?
   Recommendation: Not for Phase 1; timestamp-based expiry is sufficient for localhost.

4. **Key rotation**: Should `ccg setup` rotate the secret each time, or only on explicit `ccg endorsement rotate`?
   Recommendation: stable secret across setup; explicit rotation command.

5. **Multi-scope secret isolation**: Each scope gets its own secret (recommended) vs shared global secret?
   Recommendation: per-scope for isolation.

## 10. References

- `internal/auth/sync.go` -- Current upstream auth sync implementation
- `internal/claude/settings.go` -- Claude settings management and token injection
- `internal/provider/codex/codex.go` -- Codex provider token generation
- `internal/doctor/checks.go` -- Route proof and token validation checks
- `internal/control/active.go` -- Active scope generation management
- `internal/config/config.go` -- Scope configuration schema
- `internal/state/state.go` -- Scope state persistence
