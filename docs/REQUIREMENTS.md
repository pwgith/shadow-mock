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
| Call count | How many times a mapping has served a request within one layer scope. Drives call-count responses (FR-4.1). |
| Layer | An ordered set of mappings, call counters and a request journal. The global session, URL-based shadow mocks and key-based shadow mocks are each a layer. |
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
* **FR-1.3** Accept the WireMock JSON schema unchanged: `id`, `uuid`, `name`, `priority`, `request`, `response`, `persistent`, `metadata`, `postServeActions`, `serveEventListeners`, plus the Shadow Mock extension `callResponses`. The scenario fields (`scenarioName`, `requiredScenarioState`, `newScenarioState`) are not supported and are rejected (FR-4.4).
* **FR-1.4** Unknown fields are kept and round-tripped, not dropped, when `mappings.unknownFields` is `keep`. The default is `reject` (FR-10.9), which is what WireMock does.
* **FR-1.5** Hot reload: file changes are picked up without a restart (optional flag, on by default in standalone).

### 3.2 Request matching

* **FR-2.1** URL matching: `url`, `urlPath`, `urlPattern`, `urlPathPattern`, `urlPathTemplate` (with `pathParameters`).
* **FR-2.2** `method`, including `ANY`.
* **FR-2.3** `headers`, `queryParameters`, `cookies`, `formParameters`, `pathParameters`, `basicAuthCredentials`, `host`, `port`, `scheme`.
* **FR-2.4** Value matchers: `equalTo` (with `caseInsensitive`), `contains`, `doesNotContain`, `matches`, `doesNotMatch`, `absent`, `before`/`after`/`equalToDateTime` (with `actualFormat`, `truncateExpected`, `truncateActual`), number matchers (`equalToNumber`, `greaterThanNumber`, `greaterThanEqualNumber`, `lessThanNumber`, `lessThanEqualNumber`), `hasExactly`/`includes` (multi-value), `and`/`or`/`not` combinators.
* **FR-2.5** Body matchers: `equalTo`, `binaryEqualTo`, `contains`, `matches`, `equalToJson` (`ignoreArrayOrder`, `ignoreExtraElements`, `${json-unit.*}` placeholders), `matchesJsonPath` (with sub-matchers), `matchesJsonSchema`, `equalToXml`, `matchesXPath` (with namespaces), and multipart patterns.
* **FR-2.6** Priority: a lower number wins. Ties go to the most recently added mapping.
* **FR-2.7** Custom matchers by name, through a Go plug-in interface (replaces Java extensions).

### 3.3 Responses

* **FR-3.1** `status`, `statusMessage`, `headers`, `body`, `jsonBody`, `base64Body`, `bodyFileName`.
* **FR-3.2** Delays: `fixedDelayMilliseconds`, `delayDistribution` (`lognormal`, `uniform`), `chunkedDribbleDelay`. Global delay settings.
* **FR-3.3** Faults: `CONNECTION_RESET_BY_PEER`, `EMPTY_RESPONSE`, `MALFORMED_RESPONSE_CHUNK`, `RANDOM_DATA_THEN_CLOSE`. In SaaS behind Render's proxy, connection-level faults reach the client as proxy errors (typically 502), not raw resets (SAAS.md §9.1).
* **FR-3.4** Response templating (`transformers: ["response-template"]`, or global templating): Handlebars syntax with the WireMock helper set (`request.*`, `jsonPath`, `xPath`, `randomValue`, `now`, `date`, `regexExtract`, `math`, `base64`, `parseJson`, and the rest of the set), plus the Shadow Mock addition `request.callNumber` (FR-4.3).
* **FR-3.5** Proxying: `proxyBaseUrl`, `additionalProxyRequestHeaders`, `removeProxyRequestHeaders`, `proxyUrlPrefixToRemove`. Global browser-proxy mode is optional and lower priority.
* **FR-3.5a** Gzip response compression when the client accepts it, on by default as in WireMock. It can be disabled per mapping (`gzipDisabled`) and per org.
* **FR-3.6** Default response when nothing matches: 404 with a near-miss diagnostic body, the same as WireMock.

### 3.4 Stateful behaviour: call-count responses

Shadow Mock replaces WireMock's scenarios with **call-count responses**: a mapping can return a different response depending on how many times it has been called. This covers retries, polling, flaky dependencies and rate limits, with a much simpler model. Shadow mocks cover the other common use of scenarios, isolating tests from one another.

* **FR-4.1 Call-count responses.** A mapping may define `callResponses`, an ordered list of rules. Each rule has one selector and a `response`:
  * `"calls": "3"`: the 3rd call only.
  * `"calls": "1-2"`: calls 1 to 2.
  * `"calls": "100+"`: call 100 onwards.
  * `"every": 3`: every 3rd call (3, 6, 9, …).

  The first rule that matches the current call number wins. If none match, the mapping's `response` is used. Call numbers start at 1. Rule responses support everything a normal response does (templating, delays, faults, proxying, files).
* **FR-4.2 Counter scope.** A call is a request that the mapping matched and served. Counters are kept **per mapping, per layer scope**:
  * Each key-based or URL-based shadow mock has its own counters, including for global mappings that serve its requests.
  * Plain global traffic shares one counter per mapping.
  * A session's counters start at zero when it is created, and are discarded with it.

  Increments are atomic across engine instances, so concurrent calls get distinct, gap-free numbers. Replacing a mapping (same id) resets its counters.
* **FR-4.3 Counter admin.** The admin API lists counters, sets a mapping's counter to a value, and resets one or all counters, in the global scope or a session scope. Templates can read the call number as `{{request.callNumber}}`.
* **FR-4.4 Scenarios are not supported.** A mapping containing `scenarioName`, `requiredScenarioState` or `newScenarioState` is rejected with `422 scenarios_not_supported`, and the message suggests `callResponses`. When loading from files, such mappings are reported and skipped, never loaded with the fields ignored. The `/__admin/scenarios` endpoints return `404 not_supported`.

### 3.5 Journal, verification and diagnostics

* **FR-5.1** Request journal: record each request, the matched mapping, the response and timing. Size limit is configurable and can be turned off.
* **FR-5.2** Verification: count or find requests by pattern (`/__admin/requests/count`, `/find`, `/unmatched`).
* **FR-5.3** Near misses: closest mappings for an unmatched request, ranked by distance.
* **FR-5.4** Every journal entry records the **tenant, session and layer** that served it.
* **FR-5.5** Verification and near misses must reflect every request already served, on any instance, by the time the client receives its response. They read a shared hot journal with full bodies (Redis in SaaS).
* **FR-5.6** Journal history is kept as lean, searchable rows, with request and response bodies in object storage. Retention is per plan. Per-org settings can limit capture (`all`, `sessions-and-unmatched`, `off`) and turn off body storage.

### 3.6 Record and playback

* **FR-6.1** Recording via `/__admin/recordings/start|stop|snapshot`, producing mappings. Recording into a key-based or URL-based shadow mock must also be supported. Where WireMock offers `repeatsAsScenarios`, Shadow Mock offers `repeatsAsCallResponses`: repeated identical requests with different responses become one mapping with `callResponses`.

### 3.7 Webhooks

* **FR-6.2** `serveEventListeners` / `postServeActions` webhooks, with templating and delay.

### 3.8 Admin API

* **FR-6.3** WireMock-compatible `/__admin` REST API: mappings CRUD, `import`, `reset`, `save`, `files`, `requests`, `near-misses`, `recordings`, `settings`, `health`, `shutdown` (but not `scenarios`, FR-4.4), plus `counters` (FR-4.3).
* **FR-6.4** Admin path prefix is configurable. Shadow Mock-specific endpoints live under `/__admin/sessions/...`, plus `/__admin/mappings/validate`, `/__admin/mappings/dry-run`, `/__admin/stats/live` and `/__admin/usage`, so they never collide with WireMock's.
* **FR-6.5 SaaS restrictions.** In SaaS, `shutdown` and `save` are disabled. Global-scope writes (`mappings`, `files`, `import`, `settings`, global `reset`) are rejected with 403 because they are made through the management app (FR-9.5a). Session-scoped equivalents are allowed.

### 3.9 WireMock parity completion

Found by comparing Shadow Mock with a real WireMock 3.13.2 ([WIREMOCK_GAPS.md](WIREMOCK_GAPS.md)); the plan is in [PARITY_PLAN.md](PARITY_PLAN.md). In each case Shadow Mock must give the answer WireMock gives, unless COMPATIBILITY.md lists the difference.

* **FR-10.1 JSONPath dialect.** `matchesJsonPath` and the JSON helpers use Jayway JsonPath syntax and behaviour: filters with `==`, `!=`, `<`, `<=`, `>`, `>=`, `=~`, `in`, `nin`, `subsetof`, `anyof`, `noneof`, `contains`, `size`, `empty`, `!`, `&&` and `||`; the functions `size()`, `min()`, `max()`, `avg()`, `sum()` and `keys()`; and filters that test the root object.
* **FR-10.2 JsonUnit placeholders.** `equalToJson` supports every JsonUnit placeholder WireMock does, including `${json-unit.ignore-element}`.
* **FR-10.3 Template blocks and assignment.** `assign`, `parseJson`, `formatJson`, `formatXml`, `trim`, `contains`, `matches`, `jsonArrayAdd` and `jsonMerge` work as block helpers, and a variable set by `assign`, `val`, `parseJson`, `regexExtract` or `formData` can be read later in the same template.
* **FR-10.4 Template helper set.** `numberFormat` (named formats, `DecimalFormat` patterns, locale and options), `arrayRemove`, `isOdd`, `isEven`, `stripes`, `systemValue`, `hostname`, `lookup` with `ids[].1` keys, and the Handlebars.java string helpers (`abbreviate`, `capitalize`, `capitalizeFirst`, `center`, `cut`, `defaultIfEmpty`, `ljust`, `rjust`, `slugify`, `stringFormat`, `wordWrap`, `yesno`, `stripTags`, `dateFormat`) exist and give WireMock 3.13.2's output. `systemValue` is denied unless the key is listed in `templating.permittedSystemKeys`. `jsonSort` exists only in WireMock 4.x and is left out.
* **FR-10.5 Template value rendering.** Helpers print values as WireMock does: lists as `[a, b]`, `range` as a list, `arrayAdd` honours `position`, `arrayJoin` honours `prefix` and `suffix`, `math` does decimal arithmetic and divides at the scale of the first number, `jsonPath` gives a list for a path that names more than one place and prints objects indented and lists as compact JSON, `xPath` gives an element as indented markup, `urlEncode` honours `decode`, `parseDate` accepts RFC 1123, asctime, `unix` and `epoch`, and `truncateDate` accepts the truncations WireMock lists and refuses the rest.
* **FR-10.6 Template request model.** `request.id`, `request.path.N`, `request.requestLine.*`, `last` and numeric indexes on repeated values, `size` of a repeated value, `lookup` with `ids[].1` keys, `request.multipart` and `request.parts.<name>.body|bodyAsBase64|binary|name|headers`.
* **FR-10.7 Journal queries.** `GET /__admin/requests` honours `unmatched`, `matchingStub`, `limit` (including 0) and `since`. Journal responses carry `requestJournalDisabled`. `requests/remove` answers `{"serveEvents": [...]}`, and journal entries carry `subEvents`.
* **FR-10.8 Admin endpoints.** `requests/remove-by-metadata`, `DELETE mappings/unmatched`, `DELETE requests/{id}` (200 for an unknown id), `PUT settings`, the `proxyPassThrough` setting, `GET /__admin/docs` (OpenAPI document and Swagger page), and `GET /__admin/` as an alias for the mappings list. Stored mappings have `request.method` filled in as `ANY`, and persisted files are named `<name>-<id>.json`.
* **FR-10.9 Strict mappings.** A mapping with a field Shadow Mock does not know is rejected with 422 and a JSON pointer, as WireMock does. `mappings.unknownFields: keep` restores FR-1.4.
* **FR-10.10 Response framing.** `statusMessage` is sent as the reason phrase. `Matched-Stub-Id` and `Matched-Stub-Name` are sent on matched replies. Responses are gzipped at any size when the client accepts gzip, without `Vary`. Request bodies with `Content-Encoding: gzip` are decompressed before matching.
* **FR-10.11 Server behaviours.** Stub CORS (`--enable-stub-cors`); files in `__files` served at `/__files/<path>` and as a fallback at `/<path>`; an unreachable proxy target answers 500 with WireMock's wording.
* **FR-10.12 Recording output.** Recorded and snapshotted mappings use `urlPath` with `queryParameters`, a `name`, and text bodies as `body`; `outputFormat: IDS` returns `{"ids": [...]}`.
* **FR-10.13 Command-line flags.** Every WireMock command-line flag is accepted. Each is implemented, accepted with a startup warning because it tunes the Java server, or (until FR-10.14 and FR-10.15 are built) refused with a clear message.
* **FR-10.14 TLS and HTTP/2.** HTTPS listener with PEM, PKCS12 and JKS key material, optional client certificates, a generated self-signed certificate when none is given, HTTP/2 over TLS and in plain text, and the `--https-*`, `--disable-http`, `--disable-http2-*` and `--admin-api-require-https` flags.
* **FR-10.15 Browser proxy.** Optional browser-proxy mode with a CA keystore, `CONNECT` handling and per-host certificates. Off unless asked for.
* **FR-10.16 Extension replacements.** Built-in, opt-in `jwt` and `jwks` helpers, a Faker `random` helper, and a `graphql` body matcher.

## 4. Functional requirements: key-based shadow mocks

* **FR-7.1 Create.** `POST /__admin/sessions/key-based` returns `{id, key, expiresAt}`. The key is unguessable (at least 128 bits of entropy) and starts with a fixed prefix (default `smk_`) so humans can spot it in logs.
* **FR-7.2 TTL.** Every session has a TTL:
  * Key-based: default 1 h, maximum 24 h.
  * URL-based: default 24 h, maximum 30 days (7 days on the Free tier), because CI configuration points at a stable base URL.

  Both are configurable, and can be extended (`keepalive`) or deleted explicitly. When a session expires, its mappings, journal and call counters are discarded. An active session of either kind counts towards concurrent sessions for billing (SAAS.md §10.2).
* **FR-7.3 Session-scoped admin.** All mapping, file, journal, counter, recording and near-miss endpoints are also available under `/__admin/sessions/{id}/...`, in the same shape as the global endpoints. Calling a global endpoint with header `X-Shadow-Mock-Key: <key>` scopes it the same way. This lets existing WireMock clients be used per session with only a base-URL change.
* **FR-7.4 Detection.** For each inbound mock request, the server looks for an active session key or match token, in this order:
  1. Designated header (default `X-Shadow-Mock-Key`, configurable list) – exact lookup
  2. Designated query parameter (default `shadow_mock_key`) – exact lookup
  3. Designated cookie – exact lookup
  4. Aho-Corasick scan over all header values, the raw query string and the URL path
  5. Aho-Corasick scan over the request body (bounded, see NFR-2.3)

  The first stage that finds a hit wins, and later stages are skipped.
* **FR-7.5 Resolution.** If a session is detected, its mappings are evaluated first. If any key-based shadow mock mapping matches, it serves the response. Otherwise the request falls through to the next layer (the URL-based shadow mock, if present, then global). **The layer always takes precedence over priority**: a priority-10 key-based shadow mock mapping beats a priority-1 global mapping. Priority applies only within a layer.
* **FR-7.6 Call-count isolation.** Every session has its own call counters, starting at zero when the session is created. Requests attributed to the session advance only the session's counters, even when a global mapping serves them. One test's calls never change the call numbers another session or the global session sees, and global traffic never changes a running test's call numbers (FR-4.2).
* **FR-7.7 Override by ID.** A key-based shadow mock mapping with the same `id` as a global mapping hides that global mapping for this session.
* **FR-7.8 Suppression.** A key-based shadow mock can mask a global mapping, by ID or by pattern, without replacing it. The request then continues down the stack as if that mapping did not exist.
* **FR-7.9 Match tokens.** On create or update, a session can register extra strings (for example `"tokens": ["CUST-99812", "order-7731"]`). These are useful when the SUT does not forward the session header but does forward test data. Tokens must be unique among the tenant's active sessions; a conflict returns 409. The minimum token length is configurable (default 6) to avoid false positives.
* **FR-7.10 Ambiguity.** If an Aho-Corasick scan (metadata or body) finds keys or tokens for more than one session, the policy is configurable: `first` (earliest offset, the default), `reject` (respond 409 with a diagnostic), or `global` (ignore all of them). The journal records the ambiguity either way.
* **FR-7.11 Journal isolation.** A session's journal holds only requests attributed to it. The global journal holds everything, tagged with the session that served it.
* **FR-7.12 Propagation hint.** The response can optionally echo `X-Shadow-Mock-Matched: <id>;layer=<key-based|url-based|global>` (off by default, for debugging).

## 5. Functional requirements: URL-based shadow mocks

* **FR-8.1 Create.** `POST /__admin/sessions/url-based` returns `{id, baseUrl, expiresAt}`.
* **FR-8.2 Routing.** Path insert only: `http(s)://<host>/<org-handle>/d/<id>/...`, for example `https://shadowmock.kaizentis.xyz/my-org/d/abc12345/claims/...`. The prefix is stripped before matching, so mappings are written exactly as they would be for the real dependency. There is no host-based routing. In standalone mode the org segment is optional (`/d/<id>/...`).
* **FR-8.3 Resolution.** URL-based shadow mock mappings are evaluated first, then global. The same precedence, override, suppression and call-count isolation rules apply as for key-based shadow mocks (FR-7.5 to FR-7.8).
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
* **NFR-3.1** All persistence is behind repository interfaces: mappings, files, sessions, journal, call counters, settings, and a `BlobStore` for bulky bytes (local directory in standalone, S3-compatible object storage in SaaS, R2 recommended).
* **NFR-3.2** Implementations: `fs` (WireMock layout, standalone default), `memory` (tests and ephemeral sessions), and for SaaS:
  * `postgres`: configuration and journal rows.
  * `redis`: call counters, session registry, hot journal and change fan-out.
  * `s3`: the `BlobStore`.

  Changing the backend is a config change only.
* **NFR-3.3** The request hot path never queries the store. It works from an in-memory compiled snapshot that is refreshed by change events from the store.

### 7.4 Scalability (central server and SaaS)
* **NFR-4.1** Stateless request handling apart from caches. In SaaS, the session registry, call counters and hot journal live in a shared Redis-compatible store, so instances scale out horizontally.
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
* WebSocket, SSE and messaging mocking (WireMock 4.x beta), gRPC, and the Java-only features (JUnit and Spring integrations, WAR deployment, Java extension points)
* WireMock scenarios (stateful named state machines). Replaced by call-count responses (FR-4.1 to FR-4.4)

## 9. Decisions log
| # | Question | Decision (2026-10-26) |
|---|---|---|
| 1 | Underlay mappings in key-based shadow mocks? | No |
| 2 | Scenario state a session sees on fall-through to global | **Superseded by #16** (2026-10-27): scenarios are not supported. Sessions have their own call counters, starting at zero (FR-7.6) |
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
| 16 | Stateful behaviour (2026-10-27) | WireMock scenarios are **not supported**; mappings using them are rejected with a clear error. Replaced by **call-count responses** (`callResponses`), with counters per mapping per layer scope. Shadow mocks cover test isolation, and call counts cover retries, polling, flaky dependencies and rate limits. Cross-endpoint state is not covered; revisit if users need it |
| 17 | Unknown mapping fields (2026-10-28) | Rejected with 422 by default, as WireMock does (supersedes the keep-and-round-trip default of FR-1.4). `mappings.unknownFields: keep` restores it |
| 18 | WireMock 4.x messaging, WebSockets, SSE (2026-10-28) | Still out of scope (decision 4). Listed under Not supported in COMPATIBILITY.md |
| 19 | WireMock parity gaps (2026-10-28) | All gaps found against WireMock 3.13.2 are closed in the order of PARITY_PLAN.md, each proved by a corpus case checked against the real WireMock. TLS, HTTP/2, browser proxy and the extension replacements are in scope (FR-10.14 to FR-10.16) |

## 10. Open questions
Pricing levels in SAAS.md §12 are a proposal to validate with early customers. Items to verify before launch are listed in SAAS.md §13.
