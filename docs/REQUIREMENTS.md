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
| Managed mode | The engine run as a hosted service by an operator, with an issuer trust list, Tool app keys, shared runtime state and the obligations in section 3.10. In the Kaizentis platform documents the engine is the **Tool app** and the web application that configures it is the **Tool Management app**. `TA-n` in those documents are the requirements on the engine; section 3.10 answers each one. |
| Tool app key | A machine credential for the admin API, `smt_<id>_<secret>`, with scopes (FR-11.3). Not the same as a mock set key (`smk_`), which only the management app sees. |
| Principal | The authenticated caller: user or key, org, roles, scope. |
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
* **FR-10.9 Strict mappings.** A mapping with a field Shadow Mock does not know is rejected with 422 and a JSON pointer, as WireMock does. `mappings.unknownFields: keep` restores FR-1.4, and managed mode requires it (FR-11.1), because the management app stores mappings with fields the engine does not know and must get them back unchanged.
* **FR-10.10 Response framing.** `statusMessage` is sent as the reason phrase. `Matched-Stub-Id` and `Matched-Stub-Name` are sent on matched replies. Responses are gzipped at any size when the client accepts gzip, without `Vary`. Request bodies with `Content-Encoding: gzip` are decompressed before matching.
* **FR-10.11 Server behaviours.** Stub CORS (`--enable-stub-cors`); files in `__files` served at `/__files/<path>` and as a fallback at `/<path>`; an unreachable proxy target answers 500 with WireMock's wording.
* **FR-10.12 Recording output.** Recorded and snapshotted mappings use `urlPath` with `queryParameters`, a `name`, and text bodies as `body`; `outputFormat: IDS` returns `{"ids": [...]}`.
* **FR-10.13 Command-line flags.** Every flag in WireMock 3.13's `--help` is accepted, so a command line or Docker command written for WireMock starts. Each flag is either implemented, by setting the configuration key that does the same (the table in the configuration guide), or accepted with a start-up warning because it only tunes the Java server. A flag that needs another to mean anything (`--disable-http`, `--admin-api-require-https` and the keystore flags need `--https-port`; `--https-require-client-cert` needs `--https-truststore`; the `--ca-keystore*` and `--trust-*` flags need `--enable-browser-proxying`) stops start-up with a message that names the setting. `--proxy-all` with `--record-mappings` records live: each new request is written to `mappings/` as soon as it has been proxied. `--extensions` with the Java class of an extension Shadow Mock replaces turns the replacement on (FR-10.16); any other class warns.
* **FR-10.14 TLS and HTTP/2.** Shadow Mock can serve HTTPS and HTTP/2 as WireMock does.
  * **FR-10.14a HTTPS listener.** `tls.port` (`--https-port`) adds an HTTPS listener that serves what the mock listener serves, the admin API included unless `listen.admin` gives it a listener of its own, which stays plain HTTP. Its port must differ from the plain one, and `--disable-http` leaves only the HTTPS listener.
  * **FR-10.14b Key material.** The certificate and key come from PEM files, or from a PKCS12 or JKS keystore (passwords default to `password`, as in WireMock; a JKS key may have its own password). With none given, a self-signed certificate for `localhost`, `127.0.0.1` and `::1` is made at start-up. Material that cannot be read, or a wrong password, stops start-up and names the setting.
  * **FR-10.14c Client certificates.** A truststore (PKCS12 or JKS) makes the server ask for a client certificate and check the one it gets; `requireClientCert` turns away a client without one and needs a truststore.
  * **FR-10.14d HTTP/2.** HTTP/2 is served over TLS (negotiated by ALPN) and, in plain text, to a client that starts with it (h2c); `tls.disableHttp2Tls` and `tls.disableHttp2Plain` turn each off. The journal records `HTTP/2.0` as the protocol of an HTTP/2 request and the request has no `Host` header added.
  * **FR-10.14e Admin over HTTPS and scheme matching.** `--admin-api-require-https` answers an admin call that arrived over plain HTTP with 403 and `{"errors":[{"code":40,"title":"HTTPS is required for accessing the admin API"}]}`, and leaves mock traffic alone. A mapping can match `scheme` and `port`.
* **FR-10.15 Browser proxy.** A browser or HTTP client can use Shadow Mock as its proxy server. It is off unless asked for.
  * **FR-10.15a Switch.** `proxy.browser.enabled` (`--enable-browser-proxying`) turns it on. A `CONNECT` request when it is off is refused with 405. Tunnels are served over HTTP/1.1 only.
  * **FR-10.15b Matching and pass-through.** A request sent in absolute form, or inside a `CONNECT` tunnel, is matched against the mappings by its path whatever host it names. One that matches nothing is sent on to the origin it named, under the same rules as other proxying (`proxy.allowTargets`, `proxy.denyTargets`, `egress.blockPrivate`, `proxy.timeout`, `proxy.via`), and is recorded by a recording that is running.
  * **FR-10.15c HTTPS.** For `CONNECT`, Shadow Mock ends the client's TLS with a certificate made for the host and signed by a certificate authority the client has to trust. The authority comes from a PKCS12 or JKS keystore (`--ca-keystore*`), or is generated on first use and stored in `proxy.browser.caDir` (`~/.shadowmock` by default) with its certificate as `ca.pem`. A keystore that is missing, or whose certificate is not a certificate authority, stops start-up. A tunnel is always opened up, never relayed unread, and a host name must be letters, digits, dots, hyphens and underscores.
  * **FR-10.15d Origin certificates.** An origin's certificate is checked against the system's roots. `--trust-all-proxy-targets` accepts any, and `--trust-proxy-target` accepts those of the hosts named. A refused certificate answers 500 "SSL failure trying to make a proxied request…"; an origin that cannot be reached answers 500 "Network failure…". The trust options do not combine with `proxy.via`.
  * **FR-10.15e Journal.** The journal entry has `browserProxyRequest: true`, the scheme, host and port of the origin, and counts a request sent on as matched.
* **FR-10.16 Extension replacements.** Built-in replacements for WireMock's separately published JWT, Faker and GraphQL extensions, written from their published sources.
  * **FR-10.16a Switch.** They are off until `templating.extensions: [jwt, faker]` or `matching.extensions: [graphql]` turns them on, or `--extensions` names the Java class they replace. An unknown extension name is refused at start-up, and so are `templating.jwt` settings without `jwt`.
  * **FR-10.16b jwt.** The `jwt`, `claims`, `claimsObject` and `jwks` helpers. `{{jwt}}` signs with HS256 (the default) or RS256, with `exp` (or `maxAge` as an amount and a plural unit, 100 years by default), `iat`, `iss`, `aud`, `sub`, `nbf`, and any other argument as a claim (text, number, true or false, a list of text or whole numbers, an object). Keys come from `templating.jwt`, and what is not set is generated at start-up; the key format WireMock writes is read. `{{jwks}}` publishes the RS256 public key. A mistake in the arguments is written into the response as `[ERROR: …]`.
  * **FR-10.16c faker.** The `random` helper evaluates `Class.method` expressions (camel or snake case, with arguments) for `Name`, `Address`, `Internet`, `Company`, `Lorem`, `Number`, `PhoneNumber`, `Bool` and `Color`, from built-in word lists. An expression it does not know is written as `[ERROR: …]`.
  * **FR-10.16d graphql.** The `graphql-body-matcher` custom matcher takes `query`, `variables` and `operationName`. A request body matches when its query is the same apart from layout, comments and the order of definitions, selections, arguments, directives, variable definitions and object fields; its variables are the same JSON; and its operation name is the same. Variables or an operation name the mapping omits must be absent from the request. A query that does not parse, or is nested too deeply, does not match.

### 3.10 Managed mode (hosted service)

The Kaizentis platform hosts the engine as the **Tool app** behind its Shadow Mock management app (the **Tool Management app**). That app writes configuration to a shared Postgres database, calls the engine's admin API for runtime state, and sends the engine's users' pipelines to it. The platform's requirements (`TA-1` to `TA-16`, and the list in its decision 20) are answered here. The engine never calls the platform and never sees a Clerk token, a launch token or a mock set key. Everything below applies only in managed mode; standalone and the unmanaged central server are unchanged.

* **FR-11.1 Managed mode.** `mode: managed` is server mode (FR-9.6, `auth.driver` with issuers) plus the rules of this section. It refuses to start unless: `auth.issuers` names at least one trusted issuer; the `postgres`, `redis` and `s3` drivers are configured, with Redis requiring authentication and TLS; `admin.globalWrites` is `deny`; `mappings.unknownFields` is `keep`; `egress.blockPrivate` is on, and no setting can turn it off; and no console is enabled (FR-11.16). It also refuses `proxy.browser.enabled` and `tls.port` (a shared host terminates TLS at its edge and must not be an open proxy), and refuses the `jwt` template extension unless `templating.jwt` gives its keys, because keys generated at start-up differ between instances. `shutdown` and `save` are disabled (FR-6.5).
* **FR-11.2 Tokens from a trusted issuer.** Trust comes only from configuration: `iss`, `aud` (different for each environment) and the location of the issuer's keys (a key set URL, cached and re-read to pick up rotation, or a pinned public key). The checks run in this order and any failure answers 401 or 403 with no data: the signature against the key named by `kid` with the algorithm pinned (no `none`, no algorithm confusion); `iss` and `aud` exactly; `exp` in the future and a lifetime of at most about 2 minutes, which the engine enforces itself; `org_id` equal to the org in the path, else 403; and, when replay checking is on, a `jti` not seen before (kept in Redis). The claims read are `sub`, `org_id`, `app_role` and `scope`. No source address is trusted: the admin API is reachable from the internet and the credential is the only gate. The issuer, `sub` and `jti` are written to the audit trail (FR-11.15).
* **FR-11.3 Tool app keys.** A key is `smt_<16 base32 id>_<52 base32 secret>`. It belongs to one org and carries scopes: `sessions` (create, renew, delete and read key-based and URL-based shadow mocks and their counters), `validate` (validate and dry-run mappings) and `read` (read-only admin calls). It can never write the global layer, org settings or the journal. The engine finds it by the SHA-256 of the secret in `engine.api_keys` (hash, org, label, scopes, expiry; the management app writes the table, the engine only reads it), compares in constant time, and caches the answer for at most 60 seconds, so a revoked key stops working within a minute. The key's org must match the org in the path. The org IP allowlist applies to keys when it applies to admin calls (FR-9.6b). A `smk_` key is rejected. The engine records each key's last use and source address, batched about once a minute, in a separate table whose changes do not trigger a reload. A shadow mock or journal entry created with a key shows the key's label as its creator.
* **FR-11.4 The metadata scope.** A token may carry `scope: metadata` (a support "view as" session). Under it the engine returns no request or response bodies, no mapping or file content, no dry-run output, no near-miss detail and no exports, and allows no changes. Every admin route has a defined behaviour under the scope, a test calls each route with it, and an unlisted route is refused.
* **FR-11.5 Org state: suspended and deleted.** An org's configuration holds its plan, limits and status. A **suspended** org (non-payment, or a hold set by staff) has its mock traffic rejected with `403 org_suspended` and a `X-Shadow-Mock-Reason` header, and its keys and tokens refused; its configuration is kept and serving resumes within a second of the status changing. A **deleted** org is a tombstone: the engine answers its mock traffic, keys and tokens with `410 org_deleted` before any purge starts, creates no runtime state for it, and never recreates it. A key cached for up to 60 seconds is the only delay. Past a hard cap plus the plan's grace, mock traffic is rejected with `429 monthly_request_cap` and a JSON reason, so a failing test shows why.
* **FR-11.6 Per-org rate limit.** The engine limits each org's requests per second, with a burst, from the plan in the org's configuration. An excess request is answered `429 rate_limited` with `Retry-After` and `X-Shadow-Mock-Limit` naming the limit that applied, before its body is read. Other orgs are unaffected. Rejected requests are recorded and counted apart from served requests in the journal and stats, are not billable, and are reported per hour in the usage endpoint (FR-11.12).
* **FR-11.7 Shared runtime state.** With the `redis` driver the session registry, call counters, hot journal, stats and recordings in progress are shared, so any instance can answer any admin call and returns the whole org's view. A recording started on one instance is visible and can be stopped through any other. Live stats are aggregated across instances.
* **FR-11.8 Configuration from the database.** With the `postgres` driver the engine reads orgs, limits, mappings, file metadata and keys from the `engine` schema, whose migrations this repository owns (including `engine.api_keys`, `engine.schema_version` and the key-usage table) and whose configuration tables carry triggers that `pg_notify`. The engine listens on a session-mode connection (notifications do not work through a transaction pooler), reloads everything for the affected org when it reconnects, and also polls a configuration version every 30 seconds. A change is applied within 1 second when notifications are healthy and 30 seconds at worst, and the notification names the org and the kind of change. Readiness reports `degraded` while the listener is down. The org tables are loaded lazily and later may be limited to the orgs an instance is assigned.
* **FR-11.9 Egress.** Proxy targets, recording upstreams and webhooks are resolved first, and the engine refuses loopback, private, link-local, shared-address and cloud-metadata addresses, pins the resolved address for the connection, and allows only `http` and `https`. `egress.blockPrivate` cannot be turned off in managed mode (FR-11.1). `bodyFileName` resolves only to the org's own file rows. Redirects to blocked addresses are not followed.
* **FR-11.10 Mapping provenance.** The engine has no notion of a mock set: the org's global layer is the mappings stored for the org, and existing priority rules decide between mappings that match the same request. A mapping's `metadata.source` (set by the management app to the owning set's name) is shown as `source` in journal entries and in near-miss output for the mapping that served the request and for each candidate. Losing it loses a label, nothing more.
* **FR-11.11 Admin API for the management app.** The management app and the console are clients of the admin API and hold no behaviour of their own.
  * **FR-11.11a Sessions list.** `GET /__admin/sessions` filters by `kind`, `state` (`active` or `expired`), `createdBy`, `createdByKey` (a key's label) and `name`, newest first, with paging, and each session shows its creator, expiry and request count.
  * **FR-11.11b Journal search.** `GET /__admin/requests` also filters by `status`, `method`, `path`, `session`, `layer`, `matched` and `from`/`to` time, newest first, with `limit` and a cursor, and answers within 1 second at the 95th percentile over 500,000 rows. An entry that has no bodies says why (the org's journal settings, the size cap, or retention).
  * **FR-11.11c Stats.** `GET /__admin/stats` with `from`, `to` and `step` returns history, and `/stats/live` the present: requests per minute by layer, match rate, top unmatched paths, latency p50 and p99, detection stage hits, active sessions and expiries, and IP rejections by source (top 20).
  * **FR-11.11d Journal settings.** Each org sets journal capture (`all`, `sessions-and-unmatched` or `off`), whether bodies are stored, and whether responses are compressed, within what its plan includes.
  * **FR-11.11e Contract.** The API ships an OpenAPI description. The JSON Schema for mappings is published with each release as `schema/stub-mapping.schema.json`, and `GET /__admin/version` reports the schema version so the management app can check it against the one it vendors.
* **FR-11.12 Usage endpoint.** `GET /__admin/usage?from&to` returns hourly buckets for the org: `requests` (sum; excludes admin calls, IP rejections and rate-limited requests), `concurrentSessionsMax` (the highest count of concurrent sessions in the hour), `storedFiles` (bytes and count, the maximum), `rateLimited` and `ipRejected`. A bucket can be read again with a higher `revision` when late data arrives. The management app reads it and reports to the platform; the engine does not.
* **FR-11.13 Object storage.** The `s3` blob driver stores file content immutably and content-addressed under `files/<org_id>/<sha256>`, built from the org, never from user input. The engine fetches a file on first use with a read-only credential, checks its SHA-256, and keeps a bounded cache. Journal bodies over 64 KiB are stored under `journal/<org_id>/…`, capped at 256 KiB, with retention by plan. Standalone use is unchanged: a plain `__files` folder.
* **FR-11.14 Degraded operation.** If the object store is slow or down, the engine serves cached files and answers a request that needs an uncached file with `503` and `X-Shadow-Mock-Reason: object_store_unavailable`. If the database is down, it keeps serving from memory with its last configuration and reports degraded readiness. If Redis is down, readiness reports it and admin calls that need shared state fail with a clear 503 rather than a partial answer.
* **FR-11.15 Audit trail, purge and anonymise.** The engine keeps an audit trail of state-changing admin calls: time, org, issuer or key label, `sub`, `jti`, route and status. `GET /__admin/audit`, `POST /__admin/org/purge` and `POST /__admin/people/{id}/anonymise` need a token with the `system` scope, which only the management app holds (no org member's token and no Tool app key has it). Purge is idempotent: it deletes the org's shadow mocks, Redis keys, journal rows and stats rows and reports progress, and the org answers 410 afterwards. `POST /__admin/people/{id}/anonymise` replaces a person's identity in the audit trail and in shadow mock creator names with an anonymous marker, because the security record must stay.
* **FR-11.16 No UI in managed mode.** Managed mode serves only mock traffic, the admin API, and the health and metrics endpoints. The embedded console (FR-11.17), the API docs pages (`/__admin/docs`, `/__admin/swagger-ui`) and any other built-in page answer 404, and the engine refuses to start in managed mode with the console enabled. A test checks that no UI route answers.
* **FR-11.17 Console and command line.** *(Should have, standalone and central server only.)* The binary can serve an embedded console, a static single-page app that calls only the admin API: first the journal with near-miss analysis, session listing and deletion and dry-run, then session editing, counters and recordings. *(Could have.)* `shadowmock` has sub-commands over the admin API, for example `session create`, `journal tail` and `recording start`, taking a key or token from an environment variable in managed mode. Neither adds behaviour that the API lacks.

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
* **FR-9.3 Managed mode.** The `JWTAuthenticator` validates a bearer token against a **trusted issuer** named in the engine's configuration: the issuer name, the expected audience and the location of the issuer's public keys (a key set URL, or a pinned key). Trust is never created by data in the database. The hosted service's issuer is the management app, which signs a token of about 60 seconds for each call it makes for a user. The engine has no Clerk configuration and does not see a Clerk or launch token. The checks, claims and failure responses are FR-11.2.
* **FR-9.3a Two credential kinds.** In managed mode the admin API accepts a token from a trusted issuer (people, FR-11.2) or a Tool app key (machines, FR-11.3), and nothing else. A token and a key can never pass for each other. Org identity comes only from the token's `org_id` claim or the key's org, never from a slug.
* **FR-9.3b CI credentials.** A pipeline that creates and deletes shadow mocks uses a Tool app key. A pipeline that publishes the org's global mappings uses a *mock set key* with the management app, which writes the global layer itself; the engine never sees that key and rejects any key with the `smk_` prefix. There is no token exchange endpoint and no Clerk machine token.
* **FR-9.4 Authorisation.** An `Authorizer` checks the principal's org against the resource's tenant on every read and write. Roles:
  * `viewer`: read journals, mappings and sessions.
  * `editor`: create and manage sessions and session mappings.
  * `admin`: also global writes, settings and reset. In managed mode global writes are further limited by FR-9.5a.

  A token's `app_role` maps `admin` to `admin`, `member` to `editor` and `viewer` to `viewer`. A Tool app key's scopes map to what they allow (FR-11.3); a key is never `admin`.
* **FR-9.5 Session ownership.** Sessions record `createdBy` (the token's user, or the label of the Tool app key that made it, FR-11.3) and `tenant` (org). Any caller with the `editor` role in the org, a person with the `member` role or a key with the `sessions` scope, can create, read, modify and delete any of the org's key-based and URL-based shadow mocks. The engine needs only the principal's user or key and org for this.
* **FR-9.5a Teams and global writes.** Teams and mock sets are owned by the Kaizentis platform and its management app, and the engine never checks them. Changes to the org's global layer (mappings, files, settings) are made only through the management app, which checks its own access lists before writing to the database. With `admin.globalWrites: deny` (required in managed mode), the engine's admin API rejects global-scope writes with 403. Standalone uses `allow`, for WireMock parity.
* **FR-9.6 Org in the path.** In managed mode every engine URL starts with the org handle: `https://<tool-host>/<org-handle>/…`, for example `https://mocks.kaizentis.xyz/acme/…`. Admin calls are at `/<org-handle>/__admin/…` on the same listener. The prefix `/__` (admin, health, readiness, metrics) is reserved; health, readiness and metrics are also served at the root without an org. A handle is 3 to 40 characters of `a-z0-9-`, starts with a letter, has no trailing hyphen and is not a reserved word (`www`, `api`, `admin`, `app`, `mail`, `static` and so on), so it can never begin with `_`. The handle is Shadow Mock's own identifier, defaulted from the platform's org slug at enablement. A renamed handle keeps the old one as a permanent alias; a handle equal to another org's current handle or alias is refused; the handles and aliases of a deleted org stay reserved. The org segment is stripped before matching. Session detection always runs against that org's index only. The tenancy source is `path`; `host` is a possible later option.
* **FR-9.6a Auth split.** Everything under `/<org-handle>/__admin/` requires a credential. The credential's org must equal the org of the handle, otherwise the call fails with `403 org_mismatch`, before any handler runs and with no data in the response. Mock traffic (any HTTP method) requires no credential. Admin and mock paths have separate rate limit and body size buckets.
* **FR-9.6b Per-org IP allowlist.** Org admins can set a list of IPv4 and IPv6 CIDRs in the management app. When the list is non-empty, mock traffic from other source IPs is rejected with `403 ip_not_allowed` before detection or matching. Optionally the list also applies to the admin API; the management app calls from the host's servers, not the customer's networks, so tokens from the trusted issuer are exempt from the admin allowlist and callers with a Tool app key stay subject to it (the management app checks the user's own address before it signs a token). The client IP is the right-most entry added by a trusted proxy, with the number of trusted hops set per environment, and a client-supplied `X-Forwarded-For` is never trusted on its own, so it cannot be spoofed. Rejections are counted in stats (SAAS.md §4).
* **FR-9.7 Global session per tenant.** In SaaS, "global" means the tenant's global session. Tenants never see each other's mappings.
* **FR-9.8 Shared database.** In SaaS, a separate management app (Next.js on Vercel) writes configuration directly to the shared Postgres database. The engine must pick up those changes without being called, through database change notifications plus a periodic version check (FR-11.8, SAAS.md §6.2).

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
* **NFR-4.1** Stateless request handling apart from caches. In managed mode, the session registry, call counters, hot journal and stats live in a shared Redis-compatible store, so instances scale out horizontally and any instance answers any admin call (FR-11.7).
* **NFR-4.2** Quotas per tenant, set by plan: request rate, included and maximum concurrent sessions, mappings per session, hot journal size, retention and CI keys (SAAS.md §12.3).
* **NFR-4.3 Billing meters.** Per org, per day: mock requests (sum), concurrent sessions (95th percentile of 5-minute samples), plus visibility meters. They are reported idempotently to the platform billing service. Plan limits come from platform entitlements mirrored into the app database (SAAS.md §10, §12).
* **NFR-4.4 Bounded hot journal.** Entry caps per session and per org are set by plan: Free 200, Starter and Team 1,000, Business 5,000, with 1,000 as the default. Bodies over 64 KiB are truncated in the hot copy, so Redis memory stays proportional to plan limits.

### 7.5 Security
* **NFR-5.1** Session keys and URL-based mock IDs come from `crypto/rand`. Session keys have at least 128 bits of entropy, and URL-based mock IDs at least 80 bits (16 base32 characters by default).
* **NFR-5.2** Admin API and mock traffic can be served on separate ports or listeners.
* **NFR-5.3** Templating is sandboxed: no file system access outside `__files`, and no outbound calls except configured proxy and webhooks. SaaS adds an SSRF denylist for proxy and webhook targets.
* **NFR-5.4** Request bodies are capped to prevent memory exhaustion.
* **NFR-5.5 Browser proxy.** It is off by default. A tunnel is always terminated, never relayed unread, so every request inside it passes the allowlist, the rate limit and the proxy target rules like any other mock request. The authority's key is stored with mode 0600 (or is the keystore the user names); certificates made for hosts are kept in memory only, and no more than 1,024 at a time. Trusting origins without checking is explicit, and the guide says what is at stake.
* **NFR-5.7 Managed mode.** Trust in an issuer exists only in configuration. A credential for org A cannot read or change anything of org B on any admin route, and a test that sends one to every admin route of another org fails the build when a route is added without it. Keys and tokens are compared in constant time, a key is never logged, and a token's lifetime is capped by the engine, not the issuer.
* **NFR-5.6 Extensions.** Generated JWT secrets and keys come from `crypto/rand`. The GraphQL reader bounds nesting so a hostile request cannot exhaust the stack. A `random` expression and a `jwt` argument cannot reach the file system or the network.

### 7.6 Operability
* **NFR-6.1** Single static binary and a Docker image.
* **NFR-6.2** Configuration via YAML file, environment variables and flags, in that precedence order (flags win).
* **NFR-6.3** Structured logging (`log/slog`), Prometheus metrics (requests, matches by layer, detection stage hits, automaton size and rebuild time), and OpenTelemetry tracing.
* **NFR-6.4** `/__health` and `/__ready` endpoints, outside any org prefix. Standalone also serves `/__admin/health` for WireMock compatibility.

## 8. Out of scope (v1)
* gRPC. (GraphQL matching is an opt-in extension, FR-10.16d.)
* WireMock Java extension API (replaced by Go interfaces)
* A UI in the hosted engine. The hosted UI is the separate Shadow Mock management app (SAAS.md §1.1); the optional embedded console is for standalone use only (FR-11.17), and managed mode serves no pages (FR-11.16)
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
| 20 | Extensions and browser proxy (2026-10-30) | The jwt, faker and graphql extensions are built in and opt-in, written from the extensions' published sources because they cannot run on `wiremock-standalone` (no differential test; PARITY_PLAN.md §10). Faker uses built-in word lists rather than a Go port of Datafaker, and GraphQL a reader of our own, so no dependency is added. The browser proxy is off by default and always terminates TLS (NFR-5.5). Two Go modules were added for keystores: go-pkcs12 and keystore-go |
| 21 | Managed mode and the platform (2026-10-30) | The Kaizentis platform's Shadow Mock requirements (`TA-1` to `TA-16`) are adopted as FR-11 and FR-9.3 to FR-9.6a. This replaces the Clerk-only, JWT-only design: the engine trusts a configured issuer (the management app) and Tool app keys, and never sees Clerk, launch tokens or mock set keys. The CI token exchange (`api/ci-token.openapi.yaml`) is dropped. Usage is read from an engine endpoint, not pushed. Mock sets are a management-app concept; the engine sees mappings and a `metadata.source` label |

## 10. Open questions
Pricing levels in SAAS.md §12 are a proposal to validate with early customers. Items to verify before launch are listed in SAAS.md §13.
