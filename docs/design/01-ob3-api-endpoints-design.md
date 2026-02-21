# Design: OB3 API Endpoints for ccgateway

> Based on: [docs/research/01-ob3-api-endpoints.md](../research/01-ob3-api-endpoints.md)

## 1. Design Goals

1. **Spec compliance**: Implement the OB3 API endpoints as defined in the [Open Badges 3.0 specification](https://www.imsglobal.org/spec/ob/v3p0)
2. **Architectural consistency**: Follow ccgateway's existing patterns (registry, scope isolation, file-based state, builtin HTTP server)
3. **Minimal footprint**: Zero external dependencies; pure Go stdlib + existing ccgateway packages
4. **Scope isolation**: OB3 data lives within each vendor/profile scope directory
5. **Incremental delivery**: Phase 1 focuses on core CRUD endpoints; OAuth and advanced features follow

## 2. Architecture Overview

```
internal/
├── ob3/                          # New package: OB3 core types and storage
│   ├── types.go                  # OpenBadgeCredential, Profile, Achievement, Proof data models
│   ├── store.go                  # File-based credential/profile store (JSON)
│   └── store_test.go             # Store unit tests
├── backend/
│   └── builtin/
│       ├── server.go             # Extended: mount OB3 routes
│       ├── ob3_handler.go        # New: OB3 HTTP handlers
│       ├── ob3_handler_test.go   # New: OB3 handler tests
│       └── config.go             # Extended: OB3 config fields
├── cli/
│   └── commands.go               # Extended: OB3 subcommand (optional, phase 2)
└── doctor/
    └── doctor.go                 # Extended: OB3 health check (phase 2)
```

## 3. Data Models (`internal/ob3/types.go`)

All types follow the OB3 specification JSON-LD structure, using Go structs with JSON tags.

```go
package ob3

// OpenBadgeCredential represents a W3C Verifiable Credential for an Open Badge.
type OpenBadgeCredential struct {
    Context           []string          `json:"@context"`
    ID                string            `json:"id"`
    Type              []string          `json:"type"`
    Issuer            Profile           `json:"issuer"`
    IssuanceDate      string            `json:"issuanceDate"`
    ExpirationDate    string            `json:"expirationDate,omitempty"`
    CredentialSubject CredentialSubject  `json:"credentialSubject"`
    Proof             *Proof            `json:"proof,omitempty"`
}

// CredentialSubject links the subject to an achievement.
type CredentialSubject struct {
    ID          string      `json:"id"`
    Type        []string    `json:"type"`
    Achievement Achievement `json:"achievement"`
}

// Achievement describes the badge/achievement being awarded.
type Achievement struct {
    ID          string    `json:"id"`
    Type        []string  `json:"type"`
    Name        string    `json:"name"`
    Description string    `json:"description"`
    Criteria    Criteria  `json:"criteria"`
    Image       *Image    `json:"image,omitempty"`
}

// Criteria describes how the achievement is earned.
type Criteria struct {
    Narrative string `json:"narrative,omitempty"`
    ID        string `json:"id,omitempty"`
}

// Profile represents an issuer or subject profile.
type Profile struct {
    ID    string   `json:"id"`
    Type  []string `json:"type"`
    Name  string   `json:"name"`
    Email string   `json:"email,omitempty"`
    URL   string   `json:"url,omitempty"`
    Image *Image   `json:"image,omitempty"`
}

// Image represents badge or profile imagery.
type Image struct {
    ID      string `json:"id"`
    Type    string `json:"type"`
    Caption string `json:"caption,omitempty"`
}

// Proof contains the cryptographic proof for a verifiable credential.
type Proof struct {
    Type               string `json:"type"`
    Created            string `json:"created"`
    VerificationMethod string `json:"verificationMethod"`
    ProofPurpose       string `json:"proofPurpose"`
    ProofValue         string `json:"proofValue"`
}
```

## 4. Storage Layer (`internal/ob3/store.go`)

### 4.1 Design Decisions

- **File-based JSON storage**: Consistent with `state.json` / `config.yaml` pattern
- **Atomic writes**: Reuse the `writeAtomic` pattern from `internal/state`
- **Scope-local**: Each scope gets its own OB3 data directory at `<scopeDir>/ob3/`
- **No external database**: Keeps zero-dependency constraint

### 4.2 File Layout

```
~/.ccgateway/vendors/<vendor>/profiles/<profile>/
├── ob3/
│   ├── credentials.json    # Array of OpenBadgeCredential
│   └── profile.json        # Scope-local Profile
├── config.yaml
├── state.json
└── ...
```

### 4.3 Store Interface

```go
package ob3

// Store manages OB3 credentials and profile for a single scope.
type Store struct {
    dir string  // <scopeDir>/ob3/
}

// NewStore creates a store rooted at the given scope OB3 directory.
func NewStore(scopeDir string) *Store

// ListCredentials returns all credentials.
func (s *Store) ListCredentials() ([]OpenBadgeCredential, error)

// UpsertCredential creates or updates a credential by ID.
// If a credential with the same ID exists, it is replaced.
func (s *Store) UpsertCredential(cred OpenBadgeCredential) error

// GetProfile returns the scope profile.
func (s *Store) GetProfile() (Profile, error)

// PutProfile replaces the scope profile.
func (s *Store) PutProfile(p Profile) error
```

### 4.4 Concurrency

- File-level atomic writes (write-to-temp + rename) provide crash safety
- Read-modify-write operations use `sync.Mutex` within the `Store` instance
- Multiple ccg processes accessing the same scope simultaneously is not a supported pattern (consistent with existing ccgateway behavior)

## 5. HTTP Handlers (`internal/backend/builtin/ob3_handler.go`)

### 5.1 Route Registration

Extend the existing `Handler()` function in `server.go` to mount OB3 routes:

```go
func Handler(cfg ServeConfig) http.Handler {
    mux := http.NewServeMux()

    // Existing routes
    mux.HandleFunc("/v1/models", ...)
    mux.HandleFunc("/healthz", ...)

    // OB3 routes (phase 1)
    if cfg.OB3Enabled {
        ob3Store := ob3.NewStore(cfg.OB3DataDir)
        mountOB3Routes(mux, ob3Store)
    }

    // Catch-all
    mux.HandleFunc("/", ...)
    return mux
}
```

### 5.2 Endpoint Implementation

| Route | Method | Handler | Status Code | Response |
|---|---|---|---|---|
| `GET /ims/ob/v3p0/credentials` | GET | `handleGetCredentials` | 200 | `GetOpenBadgeCredentialsResponse` |
| `POST /ims/ob/v3p0/credentials` | POST | `handleUpsertCredential` | 200/201 | `OpenBadgeCredential` |
| `GET /ims/ob/v3p0/profile` | GET | `handleGetProfile` | 200 | `Profile` |
| `PUT /ims/ob/v3p0/profile` | PUT | `handlePutProfile` | 200 | `Profile` |
| `GET /.well-known/badgeconnect.json` | GET | `handleServiceDescription` | 200 | `ServiceDescriptionDocument` |

### 5.3 Response Envelope

```go
// GetOpenBadgeCredentialsResponse wraps the credentials list per OB3 spec.
type GetOpenBadgeCredentialsResponse struct {
    Credentials []ob3.OpenBadgeCredential `json:"credential"`
}
```

### 5.4 Error Response Format

Follow existing builtin server error pattern:

```json
{
  "error": {
    "type": "not_found",
    "message": "credential not found"
  }
}
```

Standard error types: `bad_request`, `not_found`, `method_not_allowed`, `internal_error`.

### 5.5 Pagination (Phase 1: Simple)

`GET /ims/ob/v3p0/credentials` supports:
- `?limit=N` (default: 100, max: 1000)
- `?offset=N` (default: 0)

Response headers:
- `X-Total-Count: <total>`

## 6. Configuration Changes

### 6.1 ServeConfig Extension

```go
type ServeConfig struct {
    Listen     string `json:"listen"`
    Model      string `json:"model"`
    BackendID  string `json:"backend_id"`
    OB3Enabled bool   `json:"ob3_enabled,omitempty"`
    OB3DataDir string `json:"ob3_data_dir,omitempty"`
}
```

### 6.2 Scope Paths Extension

Add to `scope.Paths`:

```go
type Paths struct {
    // ... existing fields ...
    OB3Dir string  // <scopeDir>/ob3
}
```

In `BuildPaths`:

```go
OB3Dir: filepath.Join(scopeDir, "ob3"),
```

## 7. Phased Delivery Plan

### Phase 1: Core CRUD (This PR)

- [ ] `internal/ob3/types.go` - Data models
- [ ] `internal/ob3/store.go` - File-based store
- [ ] `internal/ob3/store_test.go` - Store unit tests
- [ ] `internal/backend/builtin/ob3_handler.go` - HTTP handlers
- [ ] `internal/backend/builtin/ob3_handler_test.go` - Handler tests
- [ ] `internal/backend/builtin/server.go` - Mount OB3 routes
- [ ] `internal/backend/builtin/config.go` - OB3 config fields
- [ ] `internal/scope/scope.go` - OB3Dir path
- [ ] Update tests for config/scope changes

### Phase 2: Auth & Discovery

- [ ] OAuth 2.0 token validation middleware
- [ ] `GET /.well-known/badgeconnect.json` service description
- [ ] Dynamic client registration endpoint
- [ ] Scope-based access control

### Phase 3: Credential Integrity

- [ ] Ed25519 key pair generation per scope
- [ ] Credential signing on upsert
- [ ] Credential verification endpoint
- [ ] Proof validation in GET responses

### Phase 4: Integration

- [ ] `ccg ob3` CLI subcommand for local credential management
- [ ] Doctor check: OB3 endpoint health
- [ ] Automatic badge issuance on gateway events (optional)
- [ ] Pagination with cursor-based tokens

## 8. Testing Strategy

### Unit Tests

- `internal/ob3/store_test.go`: CRUD operations, atomic write behavior, empty store edge cases
- `internal/backend/builtin/ob3_handler_test.go`: HTTP handler tests using `httptest`, method validation, JSON encoding/decoding, error responses

### Integration Tests

- End-to-end: `ccg setup` with `ob3_enabled=true` -> verify endpoints respond
- Scope isolation: Credentials in scope A are not visible from scope B

### Test Fixtures

```go
func testCredential() ob3.OpenBadgeCredential {
    return ob3.OpenBadgeCredential{
        Context: []string{
            "https://www.w3.org/ns/credentials/v2",
            "https://purl.imsglobal.org/spec/ob/v3p0/context-3.0.3.json",
        },
        ID:   "urn:uuid:test-credential-001",
        Type: []string{"VerifiableCredential", "OpenBadgeCredential"},
        Issuer: ob3.Profile{
            ID:   "did:example:issuer",
            Type: []string{"Profile"},
            Name: "Test Issuer",
        },
        IssuanceDate: "2026-02-21T00:00:00Z",
        CredentialSubject: ob3.CredentialSubject{
            ID:   "did:example:recipient",
            Type: []string{"AchievementSubject"},
            Achievement: ob3.Achievement{
                ID:          "urn:uuid:test-achievement-001",
                Type:        []string{"Achievement"},
                Name:        "Gateway Setup Complete",
                Description: "Successfully configured ccgateway scope",
                Criteria: ob3.Criteria{
                    Narrative: "Complete ccg setup with doctor validation passing",
                },
            },
        },
    }
}
```

## 9. Design Decisions Summary

| Decision | Choice | Rationale |
|---|---|---|
| Storage backend | File-based JSON | Consistent with existing patterns; no external deps |
| Package location | `internal/ob3/` (types/store) + `builtin/ob3_handler.go` (HTTP) | Clean separation of concerns; handlers stay with the server |
| Auth (Phase 1) | None (localhost-only) | Builtin server is already bound to 127.0.0.1; auth deferred to Phase 2 |
| Credential signing | Deferred to Phase 3 | Core CRUD first; signing requires key management infrastructure |
| CLI subcommand | Deferred to Phase 4 | API-first approach; CLI wraps the API later |
| OB3 enable flag | Config-driven (`ob3_enabled`) | Opt-in; zero impact on existing gateway users |
| Route prefix | `/ims/ob/v3p0/` | Spec-compliant path prefix |
