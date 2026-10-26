# Shadow Mock: Requirements

> Status: Draft v0.2, 2026-10-26. Read with [DESIGN.md](DESIGN.md) and [SAAS.md](SAAS.md).

## 1. Purpose

Shadow Mock is an HTTP mocking server written in Go. It reproduces WireMock's functionality and adds two session models that let many tests share one mock deployment without interfering with each other:

| Mode | Who uses it | How a request is routed to it | Fallback |
|---|---|---|---|
| **Global session** | Everyone | Default: every request | None (WireMock behaviour) |
| **Key-based shadow mock** | One test or test run on a shared system-test environment | A unique **session key** found anywhere in the inbound request (header, query, cookie, path, body) | Global |
| **URL-based shadow mock** | One API under test in CI | A unique **base URL** (path insert `/<org-handle>/d/<id>`) | Global |

It runs in two deployment modes:

* **Standalone**: a single binary, file-based config, no auth (always allowed). Drop-in replacement for WireMock standalone.
* **Central server**: a shared, long-running instance used by many teams. This later becomes the **SaaS** offering (multi-tenant, JWT auth, database storage).

## 2. Glossary

| Term | Meaning |
|---|---|
| Shadow Mock | The product (this tool). In code, hosts and identifiers: `shadowmock` / `shadow-mock`. |
| Global session | The org-wide (or standalone) set of mocks, which behaves like WireMock. |
| Key-based shadow mock | A session selected by a **key** (or match token) found in the request. Kind `key-based`. |
| URL-based shadow mock | A session selected by a **URL insert** (`/<org-handle>/d/<id>/…`; `/d/<id>/…` in standalone). Kind `url-based`. |
| Session | The umbrella term in the API and code for a key-based shadow mock or a URL-based shadow mock (`/__admin/sessions/...`). |
| Mapping / stub | A request pattern plus a response definition (WireMock `StubMapping`). |
| Layer | An ordered set of mappings, scenario state and a request journal. The global session, URL-based shadow mocks and key-based shadow mocks are each a layer. |
| Session key | Opaque, unguessable token that identifies a key-based shadow mock in traffic. |
| Match token | An extra string registered on a key-based shadow mock (for example a test customer ID) that also selects that session when found in a request. |
| Tenant | An organisation (`org`) in SaaS. Standalone has one implicit tenant, `local`. |
| Principal | The authenticated caller: user, org, roles. |
| SUT | System under test. |

## 3. Functional requirements: WireMock parity (global session)

The global session must behave like WireMock 3.x, so existing mapping files and WireMock clients work unchanged.

### 3.1 Mapping file compatibility

* **FR-1.1** Load mappings from `<root>/mappings/**/*.json`. Each file holds either one mapping or `{"mappings": [...]}`.
* **FR-1.2** Load response bodies referenced by `bodyFileName` from `<root>/__files/`.
* **FR-1.3** Accept the WireMock JSON schema unchanged: `id`, `uuid`, `name`, `priority`, `request`, `response`, `persistent`, `scenarioName`, `requiredScenarioState`, `newScenarioState`, `metadata`, `postServeActions`, `serveEventListeners`.
* **FR-1.4** Unknown fields are kept and round-tripped, not dropped.
* **FR-1.5** Hot reload: file changes are picked up without a restart (optional flag, on by default in standalone).

### 3.2 Request matching

* **FR-2.1** URL matching: `url`, `urlPath`, `urlPattern`, `urlPathPattern`, `urlPathTemplate` (with `pathParameters`).
* **FR-2.2** `method`, including `ANY`.
* **FR-2.3** `headers`, `queryParameters`, `cookies`, `formParameters`, `pathParameters`, `basicAuthCredentials`, `host`, `port`, `scheme`.
* **FR-2.4** Value matchers: `equalTo` (with `caseInsensitive`), `contains`, `doesNotContain`, `matches`, `doesNotMatch`, `absent`, `before`/`after`/`equalToDateTime`, `hasExactly`/`including` (multi-value), `and`/`or`/`not` combinators.
* **FR-2.5** Body matchers: `equalTo`, `binaryEqualTo`, `contains`, `matches`, `equalToJson` (`ignoreArrayOrder`, `ignoreExtraElements`, `${json-unit.*}` placeholders), `matchesJsonPath` (with sub-matchers), `matchesJsonSchema`, `equalToXml`, `matchesXPath` (with namespaces), and multipart patterns.
* **FR-2.6** Priority: a lower number wins. Ties go to the most recently added mapping.
* **FR-2.7** Custom matchers by name, through a Go plug-in interface (replaces Java extensions).

### 3.3 Responses

* **FR-3.1** `status`, `statusMessage`, `headers`, `body`, `jsonBody`, `base64Body`, `bodyFileName`.
* **FR-3.2** Delays: `fixedDelayMilliseconds`, `delayDistribution` (`lognormal`, `uniform`), `chunkedDribbleDelay`. Global delay settings.
* **FR-3.3** Faults: `CONNECTION_RESET_BY_PEER`, `EMPTY_RESPONSE`, `MALFORMED_RESPONSE_CHUNK`, `RANDOM_DATA_THEN_CLOSE`. In SaaS behind Render's proxy, connection-level faults reach the client as proxy errors (typically 502), not raw resets (SAAS.md §9.1).
* **FR-3.4** Response templating (`transformers: ["response-template"]`, or global templating): Handlebars syntax with the WireMock helper set (`request.*`, `jsonPath`, `xPath`, `randomValue`, `now`, `date`, `regexExtract`, `math`, `base64`, `parseJson`, and the rest of the set).
* **FR-3.5** Proxying: `proxyBaseUrl`, `additionalProxyRequestHeaders`, `removeProxyRequestHeaders`, `proxyUrlPrefixToRemove`. Global browser-proxy mode is optional and lower priority.
* **FR-3.5a** Gzip response compression when the client accepts it, on by default as in WireMock. It can be disabled per mapping (`gzipDisabled`) and per org.
* **FR-3.6** Default response when nothing matches: 404 with a near-miss diagnostic body, the same as WireMock.

### 3.4 Stateful behaviour

* **FR-4.1** Scenarios: named state machines (`Started` is the initial state). Each scenario can be reset on its own or all together.
* **FR-4.2** Scenario state is **scoped to its layer** (see FR-7.6).

### 3.5 Journal, verification and diagnostics

* **FR-5.1** Request journal: record each request, the matched mapping, the response and timing. Size limit is configurable and can be turned off.
* **FR-5.2** Verification: count or find requests by pattern (`/__admin/requests/count`, `/find`, `/unmatched`).
* **FR-5.3** Near misses: closest mappings for an unmatched request, ranked by distance.
* **FR-5.4** Every journal entry records the **tenant, session and layer** that served it.
* **FR-5.5** Verification and near misses must reflect every request already served, on any instance, by the time the client receives its response. They read a shared hot journal with full bodies (Redis in SaaS).
* **FR-5.6** Journal history is kept as lean, searchable rows, with request and response bodies in object storage. Retention is per plan. Per-org settings can limit capture (`all`, `sessions-and-unmatched`, `off`) and turn off body storage.

### 3.6 Record and playback

* **FR-6.1** Recording via `/__admin/recordings/start|stop|snapshot`, producing mappings. Recording into a key-based or URL-based shadow mock must also be supported.

### 3.7 Webhooks

* **FR-6.2** `serveEventListeners` / `postServeActions` webhooks, with templating and delay.

### 3.8 Admin API

* **FR-6.3** WireMock-compatible `/__admin` REST API: mappings CRUD, `import`, `reset`, `save`, `files`, `requests`, `near-misses`, `scenarios`, `recordings`, `settings`, `health`, `shutdown`.
* **FR-6.4** Admin path prefix is configurable. Shadow Mock-specific endpoints live under `/__admin/sessions/...`, plus `/__admin/mappings/validate`, `/__admin/mappings/dry-run`, `/__admin/stats/live` and `/__admin/usage`, so they never collide with WireMock's.
* **FR-6.5 SaaS restrictions.** In SaaS, `shutdown` and `save` are disabled. Global-scope writes (`mappings`, `files`, `import`, `settings`, global `reset`) are rejected with 403 because they are made through the management app (FR-9.5a). Session-scoped equivalents are allowed.

## 4. Functional requirements: key-based shadow mocks

* **FR-7.1 Create.** `POST /__admin/sessions/key-based` returns `{id, key, expiresAt}`. The key is unguessable (at least 128 bits of entropy) and starts with a fixed prefix (default `smk_`) so humans can spot it in logs.
* **FR-7.2 TTL.** Every session has a TTL:
  * Key-based: default 1 h, maximum 24 h.
  * URL-based: default 24 h, maximum 30 days (7 days on the Free tier), because CI configuration points at a stable base URL.

  Both are configurable, and can be extended (`keepalive`) or deleted explicitly. When a session expires, its mappings, journal and scenario state are discarded. An active session of either kind counts towards concurrent sessions for billing (SAAS.md §10.2).
* **FR-7.3 Session-scoped admin.** All mapping, journal, scenario and near-miss endpoints are also available under `/__admin/sessions/{id}/...`, in the same shape as the global endpoints. Calling a global endpoint with header `X-Shadow-Mock-Key: <key>` scopes it the same way. This lets existing WireMock clients be used per session with only a base-URL change.
* **FR-7.4 Detection.** For each inbound mock request, the server looks for an active session key or match token, in this order:
  1. Designated header (default `X-Shadow-Mock-Key`, configurable list) – exact lookup
  2. Designated query parameter (default `shadow_mock_key`) – exact lookup
  3. Designated cookie – exact lookup
  4. Aho-Corasick scan over all header values, the raw query string and the URL path
  5. Aho-Corasick scan over the request body (bounded, see NFR-2.3)

  The first stage that finds a hit wins, and later stages are skipped.
* **FR-7.5 Resolution.** If a session is detected, its mappings are evaluated first. If any key-based shadow mock mapping matches, it serves the response. Otherwise the request falls through to the next layer (the URL-based shadow mock, if present, then global). **The layer always takes precedence over priority**: a priority-10 key-based shadow mock mapping beats a priority-1 global mapping. Priority applies only within a layer.
* **FR-7.6 Scenario isolation.** When a session is created, it takes its **own copy of every global scenario's state at that moment**. From then on, all reads and writes, including those made by global mappings serving the session's requests, use the session's copy. Later changes to global state are not visible to the session. One test advancing a scenario must never affect another session or the global session, and global traffic must never affect a running test. Resetting scenarios within a session sets them to `Started`, matching WireMock's reset behaviour.
* **FR-7.7 Override by ID.** A key-based shadow mock mapping with the same `id` as a global mapping hides that global mapping for this session.
* **FR-7.8 Suppression.** A key-based shadow mock can mask a global mapping, by ID or by pattern, without replacing it. The request then continues down the stack as if that mapping did not exist.
* **FR-7.9 Match tokens.** On create or update, a session can register extra strings (for example `"tokens": ["CUST-99812", "order-7731"]`). These are useful when the SUT does not forward the session header but does forward test data. Tokens must be unique among the tenant's active sessions; a conflict returns 409. The minimum token length is configurable (default 6) to avoid false positives.
* **FR-7.10 Ambiguity.** If an Aho-Corasick scan (metadata or body) finds keys or tokens for more than one session, the policy is configurable: `first` (earliest offset, the default), `reject` (respond 409 with a diagnostic), or `global` (ignore all of them). The journal records the ambiguity either way.
* **FR-7.11 Journal isolation.** A session's journal holds only requests attributed to it. The global journal holds everything, tagged with the session that served it.
* **FR-7.12 Propagation hint.** The response can optionally echo `X-Shadow-Mock-Matched: <id>;layer=<key-based|url-based|global>` (off by default, for debugging).

## 5. Functional requirements: URL-based shadow mocks

* **FR-8.1 Create.** `POST /__admin/sessions/url-based` returns `{id, baseUrl, expiresAt}`.
* **FR-8.2 Routing.** Path insert only: `http(s)://<host>/<org-handle>/d/<id>/...`, for example `https://shadowmock.kaizentis.xyz/my-org/d/abc12345/claims/...`. The prefix is stripped before matching, so mappings are written exactly as they would be for the real dependency. There is no host-based routing. In standalone mode the org segment is optional (`/d/<id>/...`).
* **FR-8.3 Resolution.** URL-based shadow mock mappings are evaluated first, then global. The same precedence, override, suppression and scenario-isolation rules apply as for key-based shadow mocks (FR-7.5 to FR-7.8).
* **FR-8.4 Session-scoped admin.** The same as FR-7.3, under `/__admin/sessions/{id}/...`.
* **FR-8.5 Nesting (phase 2).** Key-based shadow mocks can be created *inside* a URL-based shadow mock, giving the stack **key-based → URL-based → global**.
* **FR-8.6 Unknown ID.** A request to a non-existent or expired URL-based mock ID returns 404 with a clear message. It never falls back to global silently.
* **FR-8.7 Seeding.** A URL-based shadow mock or key-based shadow mock can be created with an initial set of mappings in the create payload, or by copying them from a named mapping set or folder.

## 6. Functional requirements: multi-tenancy and auth

* **FR-9.1** Every admin API call passes through an `Authenticator` that yields a `Principal{UserID, OrgID, Roles, Scopes}`.
* **FR-9.2 Standalone.** A `NoopAuthenticator` always succeeds with principal `{user: "local", org: "local", roles: [admin]}`. The checks still run, so the code paths are exercised from day one.
* **FR-9.3 SaaS.** The identity provider is Clerk. A `JWTAuthenticator` validates the signature (Clerk JWKS URL, RS256), `exp`, `nbf`, `iss` and `aud`. It reads the user from `sub` and the organisation and role from configurable claim paths (default `org_id` and `org_role`, from a Clerk JWT template). Clerk org roles are mapped to engine roles. See SAAS.md §3.2.
* **FR-9.3a JWT only.** The engine accepts only JWTs, from a configured list of trusted issuers (Clerk session JWTs for users, and Clerk M2M JWTs for CI). It never accepts opaque API keys. Org identity comes only from the JWT `org_id` claim, never from a slug.
* **FR-9.3b CI credentials.** CI authenticates with an org-scoped CI key, created in the management app, which it exchanges at an OAuth2 client-credentials endpoint for a Clerk M2M JWT of 60 minutes or less. The JWT carries `org_id`, `org_role` and `ci_key_id`. Revoking the key stops new tokens. Tokens are reused while still valid to limit Clerk mints (SAAS.md §3.6).
* **FR-9.4 Authorisation.** An `Authorizer` checks the principal's org against the resource's tenant on every read and write. Roles:
  * `viewer`: read journals, mappings and sessions.
  * `editor`: create and manage sessions and session mappings.
  * `admin`: also global writes, settings and reset. In SaaS, global writes are further limited by FR-9.5a.

  Clerk `org:member` and CI tokens map to `editor`, `org:admin` to `admin`, and an optional custom `org:viewer` role to `viewer`.
* **FR-9.5 Session ownership.** Sessions record `createdBy` (user) and `tenant` (org). Any member of the org with the `editor` role (by default every Clerk `org:member` and `org:admin`) can create, read, modify and delete any of the org's key-based and URL-based shadow mocks. The engine needs only the Clerk JWT's user and org for this.
* **FR-9.5a Teams and global writes.** Teams are owned by the Kaizentis platform (SAAS.md §3.4), and the engine never checks them. Changes to the org's global session (mappings, files, settings) are made only through the Shadow Mock management app, which checks team grants before writing to the database. With `admin.globalWrites: deny` (the SaaS setting), the engine's admin API rejects global-scope writes with 403. Standalone uses `allow`, for WireMock parity.
* **FR-9.6 Org in the path.** In SaaS, every engine URL starts with the org handle: `https://shadowmock.kaizentis.xyz/<org-handle>/…`. The handle is Shadow Mock's own immutable identifier, defaulted from the Clerk slug at onboarding. Old handles stay as aliases after a change (SAAS.md §2.2). The org segment is stripped before matching. Session detection always runs against that org's index only.
* **FR-9.6a Auth split.** Everything under `/<org-handle>/__admin/` requires a JWT. The JWT's `org_id` must equal the org of the handle, otherwise the call fails with `403 org_mismatch`. Mock traffic (any HTTP method) requires no JWT.
* **FR-9.6b Per-org IP allowlist.** Org admins can set a list of IPv4 and IPv6 CIDRs in the management app. When the list is non-empty, mock traffic from other source IPs is rejected with `403 ip_not_allowed` before detection or matching. Optionally the list also applies to the admin API. The client IP is taken only from trusted-proxy `X-Forwarded-For` hops, so it cannot be spoofed. Rejections are counted in stats (SAAS.md §4).
* **FR-9.7 Global session per tenant.** In SaaS, "global" means the tenant's global session. Tenants never see each other's mappings.
* **FR-9.8 Shared database.** In SaaS, a separate management app (Next.js on Vercel) writes configuration directly to the shared Postgres database. The engine must pick up those changes without being called, through database change notifications plus periodic reconciliation (SAAS.md §6.2).

## 7. Non-functional requirements

### 7.1 Compatibility
* **NFR-1.1** WireMock mapping JSON and admin API compatibility, verified by a conformance test suite that replays WireMock's own documentation examples.
* **NFR-1.2** Works with existing WireMock client libraries (Java, .NET, JS, Python) pointed at Shadow Mock.

### 7.2 Performance
* **NFR-2.1** Added matching latency is p99 under 2 ms with 5,000 global mappings plus 50 key-based shadow mock mappings, on one core.
* **NFR-2.2** Session detection runs in linear time in the request size, independent of the number of active sessions (Aho-Corasick).
* **NFR-2.3** The body scan is bounded by `maxScanBytes` (default 1 MiB) and limited to configurable content types (default: text/*, JSON, XML, form, multipart text parts). gzip and deflate bodies are optionally decompressed before the scan.
* **NFR-2.4** Creating or deleting a session takes effect within 100 ms. Automaton rebuilds are copy-on-write and never block in-flight requests.
* **NFR-2.5** Target throughput: at least 10k req/s per instance for simple stubs.

### 7.3 Storage abstraction
* **NFR-3.1** All persistence is behind repository interfaces: mappings, files, sessions, journal, scenarios, settings, and a `BlobStore` for bulky bytes (local directory in standalone, S3-compatible object storage in SaaS, R2 recommended).
* **NFR-3.2** Implementations: `fs` (WireMock layout, standalone default), `memory` (tests and ephemeral sessions), and for SaaS:
  * `postgres`: configuration and journal rows.
  * `redis`: scenario state, session registry, hot journal and change fan-out.
  * `s3`: the `BlobStore`.

  Changing the backend is a config change only.
* **NFR-3.3** The request hot path never queries the store. It works from an in-memory compiled snapshot that is refreshed by change events from the store.

### 7.4 Scalability (central server and SaaS)
* **NFR-4.1** Stateless request handling apart from caches. In SaaS, the session registry, scenario state and hot journal live in a shared Redis-compatible store, so instances scale out horizontally.
* **NFR-4.2** Quotas per tenant, set by plan: request rate, included and maximum concurrent sessions, mappings per session, hot journal size, retention and CI keys (SAAS.md §12.3).
* **NFR-4.3 Billing meters.** Per org, per day: mock requests (sum), concurrent sessions (95th percentile of 5-minute samples), plus visibility meters. They are reported idempotently to the platform billing service. Plan limits come from platform entitlements mirrored into the app database (SAAS.md §10, §12).
* **NFR-4.4 Bounded hot journal.** Entry caps per session and per org are set by plan: Free 200, Starter and Team 1,000, Business 5,000, with 1,000 as the default. Bodies over 64 KiB are truncated in the hot copy, so Redis memory stays proportional to plan limits.

### 7.5 Security
* **NFR-5.1** Session keys and URL-based mock IDs come from `crypto/rand`. Session keys have at least 128 bits of entropy, and URL-based mock IDs at least 80 bits (16 base32 characters by default).
* **NFR-5.2** Admin API and mock traffic can be served on separate ports or listeners.
* **NFR-5.3** Templating is sandboxed: no file system access outside `__files`, and no outbound calls except configured proxy and webhooks. SaaS adds an SSRF denylist for proxy and webhook targets.
* **NFR-5.4** Request bodies are capped to prevent memory exhaustion.

### 7.6 Operability
* **NFR-6.1** Single static binary and a Docker image.
* **NFR-6.2** Configuration via YAML file, environment variables and flags, in that precedence order (flags win).
* **NFR-6.3** Structured logging (`log/slog`), Prometheus metrics (requests, matches by layer, detection stage hits, automaton size and rebuild time), and OpenTelemetry tracing.
* **NFR-6.4** `/__health` and `/__ready` endpoints, outside any org prefix. Standalone also serves `/__admin/health` for WireMock compatibility.

## 8. Out of scope (v1)
* gRPC and GraphQL-specific matching (only HTTP-level matching in v1)
* WireMock Java extension API (replaced by Go interfaces)
* A UI in the engine. The engine is API-only, and the SaaS UI is the separate Shadow Mock management app (SAAS.md §1.1)
* "Underlay" session mappings that apply only when no global mapping matches. Session mappings are overrides only.
* Scanning **response** bodies of proxied calls for session keys
* WebSocket and SSE mocking

## 9. Decisions log
| # | Question | Decision (2026-10-26) |
|---|---|---|
| 1 | Underlay mappings in key-based shadow mocks? | No |
| 2 | Scenario state a session sees on fall-through to global | The session's own copy, snapshotted at session creation, for reads and writes (FR-7.6) |
| 3 | Body-scan proxied response bodies? | No |
| 4 | WebSocket/SSE mocking? | No |
| 5 | SaaS addressing for mock traffic | One host, `shadowmock.kaizentis.xyz`, org handle as the first path segment, URL-based shadow mocks at `/<org-handle>/d/<id>`. No per-tenant DNS |
| 6 | SaaS platform | Next.js + Clerk on Vercel. A separate Shadow Mock management app (Next.js, Vercel) shares the database with the Go engine. The engine has no UI and is auto-scaled with a shared Redis-compatible cache. See SAAS.md |
| 7 | Org identity | Management apps use Clerk slugs. The engine uses an immutable org handle in the path, and on admin calls checks it against the JWT `org_id` |
| 8 | Engine auth | Creating and managing sessions needs the Clerk JWT (user + org). Mock traffic needs none |
| 9 | Network restriction | Optional per-org IP allowlist for mock traffic, managed in the management app |
| 10 | CI machine credentials | Option C: CI key → short-lived Clerk M2M JWT through a management-app exchange endpoint. The engine trusts only Clerk |
| 11 | Bulky data | Object storage (S3 API, Cloudflare R2 recommended) for journal bodies, `__files`, recordings and exports. Postgres keeps lean journal rows with pointers |
| 12 | Hosting | Engine on Render (autoscaled, min 2 instances); Render Key Value for Redis; Supabase for Postgres (Pro before production); Cloudflare R2 for objects. One portable Docker image, no AWS |
| 13 | Cost-driven design | (A) shared per-instance journal body objects; (B) bounded hot journal; (C) gzip by default; (D) Clerk per-org fees are a platform cost, charged once per org. See SAAS.md §11 |
| 14 | Pricing structure | Platform org fee (Free, Team $19, Business $79, Enterprise) plus per-app tiers. Shadow Mock: Free, Starter $29, Team $99, Business $399, Enterprise, with overage on requests and p95 concurrent sessions. Proposal; see SAAS.md §12 |
| 15 | Session TTLs | Key-based: default 1 h, max 24 h. URL-based: default 24 h, max 30 days. Both renewable, and both count as concurrent sessions while active (FR-7.2) |

## 10. Open questions
Pricing levels in SAAS.md §12 are a proposal to validate with early customers. Items to verify before launch are listed in SAAS.md §13.
