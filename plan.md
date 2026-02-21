# Implementation Plan: OB3 API Endpoints for ccgateway

> Research: `docs/research/01-ob3-api-endpoints.md`
> Design: `docs/design/01-ob3-api-endpoints-design.md`

## Phase 1 Implementation Steps (Core CRUD)

### Step 1: Data Models (`internal/ob3/types.go`)
Create the `internal/ob3/` package with OB3 data model types:
- `OpenBadgeCredential` (W3C VC with OB3 extensions)
- `CredentialSubject`, `Achievement`, `Criteria`
- `Profile`, `Image`, `Proof`
- `GetOpenBadgeCredentialsResponse` (list response wrapper)

### Step 2: File-Based Store (`internal/ob3/store.go`)
Implement the credential/profile store:
- `Store` struct with scope-local directory (`<scopeDir>/ob3/`)
- `ListCredentials()` - read and return all credentials
- `UpsertCredential(cred)` - create or update by ID (atomic write)
- `GetProfile()` / `PutProfile(p)` - scope profile CRUD
- Atomic file writes via temp+rename pattern (consistent with `state.go`)

### Step 3: Store Tests (`internal/ob3/store_test.go`)
- CRUD round-trip tests
- Upsert overwrites existing credential with same ID
- Empty store returns empty list (not error)
- Profile get on empty store returns zero-value
- Concurrent write safety (mutex)

### Step 4: HTTP Handlers (`internal/backend/builtin/ob3_handler.go`)
Implement OB3 REST endpoint handlers:
- `GET /ims/ob/v3p0/credentials` -> `handleGetCredentials` (with `?limit=&offset=` pagination)
- `POST /ims/ob/v3p0/credentials` -> `handleUpsertCredential`
- `GET /ims/ob/v3p0/profile` -> `handleGetProfile`
- `PUT /ims/ob/v3p0/profile` -> `handlePutProfile`
- `GET /.well-known/badgeconnect.json` -> `handleServiceDescription` (static discovery document)
- Method validation, JSON encoding, error responses

### Step 5: Handler Tests (`internal/backend/builtin/ob3_handler_test.go`)
- `httptest`-based tests for all 5 endpoints
- Verify correct status codes, response bodies, method restrictions
- Test pagination query params
- Test upsert create vs update behavior

### Step 6: Server Integration (`internal/backend/builtin/server.go`, `config.go`)
- Extend `ServeConfig` with `OB3Enabled` and `OB3DataDir` fields
- Extend `Handler()` to conditionally mount OB3 routes when `ob3_enabled=true`
- Update config load/save for new fields

### Step 7: Scope Paths Extension (`internal/scope/scope.go`)
- Add `OB3Dir` field to `Paths` struct
- Set it in `BuildPaths()` to `<scopeDir>/ob3`

### Step 8: Update Existing Tests
- Verify `ServeConfig` backward compatibility (new fields are `omitempty`)
- Verify `Paths` struct changes don't break existing tests
- Run full test suite

## Files Changed (Phase 1)

| File | Action | Description |
|---|---|---|
| `internal/ob3/types.go` | **New** | OB3 data models |
| `internal/ob3/store.go` | **New** | File-based store |
| `internal/ob3/store_test.go` | **New** | Store tests |
| `internal/backend/builtin/ob3_handler.go` | **New** | HTTP handlers |
| `internal/backend/builtin/ob3_handler_test.go` | **New** | Handler tests |
| `internal/backend/builtin/server.go` | **Modified** | Mount OB3 routes |
| `internal/backend/builtin/config.go` | **Modified** | OB3 config fields |
| `internal/backend/builtin/config_test.go` | **Modified** | Config test updates |
| `internal/scope/scope.go` | **Modified** | Add OB3Dir path |

## Risks & Mitigations

| Risk | Mitigation |
|---|---|
| File-based store doesn't scale | Phase 1 targets single-scope local use; SQLite can be added in Phase 2 |
| No auth on OB3 endpoints | Server binds to 127.0.0.1 only; OAuth deferred to Phase 2 |
| Breaking existing tests | All new fields use `omitempty`; backward-compatible changes only |
