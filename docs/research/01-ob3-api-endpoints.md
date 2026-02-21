# Research: Open Badges 3.0 (OB3) API Endpoints for ccgateway

## 1. Background

### 1.1 What is Open Badges 3.0?

Open Badges 3.0 (OB3) is the latest version of the 1EdTech (formerly IMS Global) Open Badges specification. It aligns with the W3C Verifiable Credentials Data Model v2.0, enabling cryptographically verifiable digital credentials. Unlike OB2 which relied on hosted JSON documents, OB3 uses signed verifiable credentials that can be verified independently of the issuer's continued online presence.

**Key differences from OB2:**
- Credentials are W3C Verifiable Credentials (VCs)
- Supports Decentralized Identifiers (DIDs) for issuer/recipient identification
- Cryptographic proof replaces hosted verification
- RESTful API with OAuth 2.0 replaces the simpler baked/hosted model

### 1.2 Relevance to ccgateway

ccgateway currently routes LLM API traffic through scoped vendor/profile configurations. Adding OB3 API endpoints enables ccgateway to:

1. **Issue achievement credentials** for completed tasks, code reviews, or milestones
2. **Host and serve badges** earned through the gateway's managed workflows
3. **Verify credentials** presented by external systems
4. **Integrate with the existing backend registry** pattern as a new backend capability

## 2. OB3 Specification API Endpoints

### 2.1 Service Discovery

| Endpoint | Method | Path | Auth | Description |
|---|---|---|---|---|
| `getServiceDescription` | `GET` | `/.well-known/badgeconnect.json` | None | Returns ServiceDescriptionDocument with API capabilities, security schemes, and endpoint URLs |

The ServiceDescriptionDocument is an OpenAPI 3.0 document that describes:
- Available API endpoints
- OAuth 2.0 security scheme (`authorizationUrl`, `tokenUrl`, `refreshUrl`)
- Dynamic client registration URL (`x-imssf-registrationUrl`)
- Supported scopes and permissions

### 2.2 Credential Endpoints

| Endpoint | Method | Path | Auth | Description |
|---|---|---|---|---|
| `getCredentials` | `GET` | `/ims/ob/v3p0/credentials` | OAuth 2.0 | Retrieve credentials for authenticated user. Supports pagination query params. Each element MUST be verifiable. |
| `upsertCredential` | `POST` | `/ims/ob/v3p0/credentials` | OAuth 2.0 | Create or update an OpenBadgeCredential. Returns the persisted credential. |

**Credential format:** `OpenBadgeCredential` - a W3C Verifiable Credential with:
- `@context`: JSON-LD context including OB3 context
- `type`: `["VerifiableCredential", "OpenBadgeCredential"]`
- `issuer`: DID or URL identifying the issuer
- `credentialSubject`: Contains `achievement` with badge class details
- `proof`: Cryptographic proof (embedded or external)

### 2.3 Profile Endpoints

| Endpoint | Method | Path | Auth | Description |
|---|---|---|---|---|
| `getProfile` | `GET` | `/ims/ob/v3p0/profile` | OAuth 2.0 | Fetch profile for the authenticated entity |
| `putProfile` | `PUT` | `/ims/ob/v3p0/profile` | OAuth 2.0 | Update profile for the authenticated entity |

### 2.4 Authentication & Authorization

The OB3 API uses OAuth 2.0 Authorization Code Grant with:
- **Dynamic Client Registration** (RFC 7591)
- **Scopes**: Granular resource-based permissions for read/write operations
- **Token lifecycle**: Access token, refresh token, revocation

### 2.5 Ecosystem Roles

| Role | Description | Required Endpoints |
|---|---|---|
| **Issuer** | Creates and delivers OpenBadgeCredentials | `upsertCredential` (consumer-side) |
| **Host (Service Provider)** | Aggregates and hosts credentials; controls API access | All endpoints (server-side) |
| **Displayer (Service Consumer)** | Displays and verifies badges | `getCredentials`, `getProfile` (consumer-side) |
| **Verifier** | Verifies credential authenticity | Verification logic (no dedicated endpoint; uses crypto proof) |

## 3. Analysis: ccgateway Integration Points

### 3.1 Current Architecture

```
ccgateway architecture:
├── cmd/ccg/           # CLI entry point
├── internal/
│   ├── backend/       # Backend registry + interface (Bundle, ArtifactInstaller, ProxyRenderer, HealthChecker)
│   │   ├── builtin/   # Built-in HTTP server backend (server.go has /v1/models, /healthz)
│   │   └── cliproxyapi/ # External proxy backend
│   ├── cli/           # Command routing (commands.go)
│   ├── config/        # Scope configuration (RuntimeMode, SettingsLayer, etc.)
│   ├── provider/      # Vendor provider registry (AuthStrategy, ClaudePatcher)
│   ├── scope/         # Scope management (Ref, Paths)
│   ├── service/       # Service lifecycle (install, start, stop)
│   └── ...
```

### 3.2 Alignment with Existing Patterns

The ccgateway codebase uses a **registry pattern** for both backends and providers. OB3 endpoints can be integrated via:

1. **New backend capability**: Extend `backend.Capability` with OB3-specific capabilities
2. **New HTTP routes in builtin server**: Add OB3 API routes alongside existing `/v1/models` and `/healthz`
3. **Separate OB3 module**: Self-contained `internal/ob3/` package with its own data models, handlers, and storage

### 3.3 Data Model Requirements

From the OB3 spec, the following data models are needed:

```
OpenBadgeCredential (W3C Verifiable Credential):
├── @context: []string
├── id: string (URI)
├── type: []string  ["VerifiableCredential", "OpenBadgeCredential"]
├── issuer: Profile (or URI)
├── issuanceDate: datetime
├── credentialSubject:
│   ├── id: string (DID or URI)
│   └── achievement:
│       ├── id: string (URI)
│       ├── type: []string  ["Achievement"]
│       ├── name: string
│       ├── description: string
│       ├── criteria: Criteria
│       └── image: Image (optional)
└── proof: Proof (cryptographic signature)

Profile:
├── id: string (DID or URI)
├── type: []string  ["Profile"]
├── name: string
├── email: string (optional)
├── url: string (optional)
└── image: Image (optional)

ServiceDescriptionDocument:
├── openapi: "3.0.0"
├── info: { title, version, ... }
├── paths: { endpoint definitions }
└── components:
    └── securitySchemes:
        └── OAuth2: { authorizationUrl, tokenUrl, ... }
```

### 3.4 Storage Considerations

Options for credential persistence within ccgateway's scope-based model:

| Option | Pros | Cons |
|---|---|---|
| **File-based (JSON per scope)** | Consistent with existing state.json pattern; no external deps | Limited query capability; no concurrent write safety beyond file locks |
| **SQLite per scope** | Rich queries; ACID transactions; single file | New dependency; migration management |
| **In-memory + file snapshot** | Fast; simple | Data loss on restart; not suitable for production hosting |

**Recommendation:** File-based JSON storage in the scope directory (`<scopeDir>/ob3/credentials.json`), consistent with how ccgateway already manages `state.json` and `config.yaml`. SQLite can be considered for v2 if query complexity grows.

### 3.5 Security Considerations

1. **OAuth 2.0 scope**: ccgateway already manages auth tokens per scope (`auth sync`). OB3 OAuth can leverage the same auth infrastructure.
2. **Credential signing**: Requires cryptographic key management. Options:
   - Ed25519 key pair stored in scope directory
   - Integration with external key management
3. **Access control**: OB3 endpoints should respect the existing vendor/profile scope boundaries.

## 4. Related Specifications

- [W3C Verifiable Credentials Data Model v2.0](https://www.w3.org/TR/vc-data-model-2.0/)
- [Open Badges 3.0 Specification](https://www.imsglobal.org/spec/ob/v3p0)
- [Open Badges 3.0 Implementation Guide](https://www.imsglobal.org/spec/ob/v3p0/impl)
- [Open Badges 3.0 Certification Guide](https://www.imsglobal.org/spec/ob/v3p0/cert)
- [OAuth 2.0 Authorization Code Grant (RFC 6749)](https://tools.ietf.org/html/rfc6749)
- [OAuth 2.0 Dynamic Client Registration (RFC 7591)](https://tools.ietf.org/html/rfc7591)

## 5. Open Questions

1. **Scope of implementation**: Should ccgateway act as a full OB3 Host (Service Provider), or primarily as an Issuer that delegates hosting?
2. **Key management**: How should signing keys be provisioned and rotated per scope?
3. **Integration depth**: Should OB3 credentials be automatically issued on certain gateway events (e.g., successful model switch, failover completion), or only via explicit API calls?
4. **Compliance target**: Is 1EdTech certification conformance required, or is spec-compatible implementation sufficient?
5. **Multi-scope badges**: Can credentials reference achievements across vendor/profile boundaries?
