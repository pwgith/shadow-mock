# WireMock parity plan

This plan closes the gaps listed in [WIREMOCK_GAPS.md](WIREMOCK_GAPS.md). Each gap gets a requirement
(FR-10.x in [REQUIREMENTS.md](REQUIREMENTS.md)), a design note in [DESIGN.md](DESIGN.md), a Gherkin
feature, a corpus case checked against real WireMock, unit tests, and an entry in the user guide or
[COMPATIBILITY.md](COMPATIBILITY.md).

## Method: red, then green, against the real WireMock

The reason the gaps existed is that our expected results came from our own engine. For every gap:

1. **Probe.** Send the case to real WireMock 3.13.2 and record what it does (the probe scripts did this).
2. **Corpus case.** Add the mapping to `corpus/wiremock/mappings/16-parity/` and the request to
   `parityCases()` in `test/conformance/wiremock_test.go`. Run the differential test. It fails.
3. **Feature.** Write the scenarios in `features/01_wiremock_parity/`, tagged `@P3 @FR-10.x`.
   They fail.
4. **Implement** until the corpus case, the feature and the unit tests pass, and the differential
   test agrees with WireMock.
5. **Document.** Tick the row in WIREMOCK_GAPS.md, update the guide and COVERAGE.md, and list any
   behaviour we deliberately keep different in COMPATIBILITY.md (the test requires this).

CI already runs the differential test in the `wiremock-reference` job, so each workstream is held in
place after it merges.

Cases that depend on the clock, random values or the machine's name are compared by shape (a regular
expression the corpus case supplies), not by exact text.

## Workstreams

One branch and one pull request each, in this order. Order is by how often a WireMock user hits the gap.

| # | Workstream | Requirements | Gaps closed (WIREMOCK_GAPS.md) | Size |
|---|---|---|---|---|
| 1 | JSONPath that behaves like Jayway, and complete JsonUnit placeholders | FR-10.1, FR-10.2 | §2 | M |
| 2 | Templating: block helpers and assignment | FR-10.3 | §3 block forms | M |
| 3 | Templating: helper set, value rendering, request model | FR-10.4, FR-10.5, FR-10.6 | §3 helpers, behaviour, request model | L |
| 4 | Admin API: journal filters, shapes, missing endpoints, strict mapping validation | FR-10.7, FR-10.8, FR-10.9 | §4 | M |
| 5 | Wire behaviour: status message, `Matched-Stub-Id`, gzip, CORS, file serving, proxy errors | FR-10.10, FR-10.11 | §5 (except TLS and HTTP/2) | M |
| 6 | Command-line flags | FR-10.13 | §1 | M |
| 7 | Recording and snapshot output | FR-10.12 | §4 snapshot, §5 recorded shape | S |
| 8 | TLS and HTTP/2 | FR-10.14 | §5 HTTP/2, HTTPS, `--https-*` flags | L |
| 9 | Browser proxy mode | FR-10.15 | §5 browser proxy | L |
| 10 | Built-in replacements for the common extensions (JWT, Faker, GraphQL) | FR-10.16 | §6 | M |

Messaging, WebSockets and Server-Sent Events (WireMock 4.x beta) stay out of scope (decision 4). The
Java-only features (JUnit, Spring, WAR deployment, Java extension points) stay out of scope. Both are
now listed under "Not supported" in COMPATIBILITY.md so the answer is written down.

### 1. JSONPath and JsonUnit (FR-10.1, FR-10.2)

* **Cause.** `matchesJsonPath` uses a library that follows the standard (RFC 9535), while WireMock uses
  Jayway JsonPath, whose filters and functions differ.
* **Design.** A small JSONPath package of our own (`internal/jsonpath`) that implements Jayway's syntax:
  dot and bracket children, wildcard, deep scan, indexes (negative too), slices, unions, filters
  (`==`, `!=`, `<`, `<=`, `>`, `>=`, `=~` with flags, `in`, `nin`, `subsetof`, `anyof`, `noneof`,
  `contains`, `size`, `empty`, `!`, `&&`, `||`, nested `@` and `$` references), and the functions
  `size()`, `length()`, `min()`, `max()`, `avg()`, `sum()`, `keys()`. A filter written against a root
  object tests that object, as Jayway does. `matchesJsonPath` and the templating helpers (`jsonPath`,
  `jsonRemove`, `jsonSort`, `jsonArrayAdd`) share it, so they cannot disagree.
* **JsonUnit.** Add the placeholders still missing (`${json-unit.ignore-element}`, `.matches:`, number
  and type placeholders with the `${json-unit.any-*}` spellings) to the `equalToJson` comparer.
* **Tests.** The 42 JSONPath probe expressions become table-driven unit tests and corpus cases, with
  both servers expected to agree. A fuzz test checks that no expression panics.
* **Done when.** Every row in WIREMOCK_GAPS.md §2 is closed and `length()` (which WireMock itself
  rejects) is listed in COMPATIBILITY.md as a deliberate difference or made to behave the same.

### 2. Templating blocks and assignment (FR-10.3)

* **Cause.** The template engine renders a block helper's body, but `assign` writes the variable into a
  scope that is gone when the block ends, and several block helpers expect a parameter where WireMock
  reads the block body.
* **Design.** A per-render variable store on the render context (a map shared by the whole template),
  written by `assign`, `val ... assign=`, `parseJson`, `regexExtract ... 'name'` and `formData`, and read
  as a normal variable. Block helpers (`assign`, `parseJson`, `formatJson`, `formatXml`, `trim`,
  `contains`, `matches`, `jsonArrayAdd`, `jsonMerge`) take their input from the block body when called
  as a block. `contains` and `matches` return a boolean in a sub-expression and render the block when
  true.
* **Tests.** Every `handlebars` example in WireMock's response-templating page is run on both servers by
  a new conformance test (`TestTemplatingDocExamples`), comparing output after whitespace is removed.
  Examples that depend on random values or the clock are skipped by name, with the reason.
* **Done when.** Each doc example either agrees with WireMock or is listed in the skip table with a reason.

### 3. Templating helpers, rendering and request model (FR-10.4 to FR-10.6)

**Status: done.** What the probes of WireMock 3.13.2 changed from the list below:

* `jsonSort` is WireMock 4.x only and is not built. `numberFormat` has no `locale=` or `maxFractionDigits=`
  option; it takes the locale as its third parameter and the options are the Java names
  (`maximumFractionDigits`, `groupingUsed`, `roundingMode` and so on).
* `parseDate` does not read RFC 1036, and `truncateDate` has nine names, not "every truncation".
* Negative indexes are off by one in WireMock and are not copied. `last` is.
* `jsonRemove` and `jsonArrayAdd jsonPath=` still use the old JSONPath library; moving them needs a remove
  operation in `internal/jsonpath`, which goes with a later workstream.
* The engine could not print a list through its String method, so every output tag now passes its value
  through `printValue`, and helpers with optional parameters are chosen by how many parameters the call has.
* The helper probes are recorded in `internal/respond/template/testdata/wiremock_helpers.json` (530 cases,
  62 skipped with a reason), and eleven corpus cases cover the rest on both servers.

The plan as first written:

* **Helpers added:** `numberFormat` (predefined formats, DecimalFormat patterns, locale, digits,
  grouping, rounding), `arrayRemove`, `isOdd`, `isEven`, `stripes`, `systemValue` (denied unless allowed
  by `templating.permittedSystemKeys`, as WireMock denies by default), `formatXml` (helper and block),
  `jsonSort` (WireMock 4.x), the Handlebars.java string helpers (`abbreviate`, `capitalizeFirst`,
  `center`, `cut`, `defaultIfEmpty`, `ljust`, `rjust`, `slugify`, `stringFormat`, `wordWrap`, `yesno`,
  `stripTags`, `dateFormat`).
* **Rendering rules.** Lists print as `[a, b]` (Java `toString`), `range` returns a list, `arrayAdd`
  honours `position`, `arrayJoin` honours `prefix` and `suffix`, `math` divides integers as integers when
  both operands are integers, `jsonPath` returns a list as JSON, `xPath` returns an element as markup,
  `urlEncode` honours `decode`, `hostname` returns the machine name, `parseDate` accepts RFC 1123,
  RFC 1036, asctime, `unix` and `epoch`, `truncateDate` accepts every truncation in WireMock's list.
* **Request model:** `request.id`, `request.path.N`, `request.requestLine`, negative and `last` indexes,
  `lookup request.query 'ids[].1'`, `request.multipart`, `request.parts.<name>.*`.
* **Tests.** The helper probes (t1, t2, t3, t12) become corpus cases in `16-parity/templating`; each
  helper also gets a unit test and a scenario in `template_helpers.feature`.

### 4. Admin API (FR-10.7 to FR-10.9)

**Status: done.** What the probes of WireMock 3.13.2 changed from the list below:

* `meta.total` counts what `unmatched` and `matchingStub` leave, before `since` and `limit` cut the list.
  `unmatched` is read as WireMock reads a boolean (anything but `true` is false), and both filters apply
  together. A bad `matchingStub` or `since` is a 400 on both servers. A bad `limit` is a 500 in WireMock and
  a 400 here.
* `requests/remove-by-metadata` takes a value pattern and removes the entries a stub with matching metadata
  served. `DELETE mappings/unmatched` removes the stubs no journal entry names.
* `proxyPassThrough` starts true. A `POST` or `PUT` of settings replaces every setting but keeps
  `proxyPassThrough` when the body leaves it out.
* `GET /__admin` and `/__admin/swagger-ui` redirect with a slash. The Swagger page loads its script from a
  CDN; the OpenAPI document is embedded.
* The file name of a persisted mapping is the slug of its name, or `<method>-<url>` when it has none, then
  the id. A mapping that is replaced keeps its first file name, as in WireMock.
* Strict mappings check every part of a mapping (the lists are in `internal/model/strict.go`). Matchers
  without an operator are always refused. `hasExactly` and `includes` stay valid in headers and query
  parameters.
* WireMock cannot delete mappings it loaded from files (`DELETE /mappings/unmatched` answers 500), so the
  differential test compares mapping counts only against a WireMock without file mappings.

The plan as first written:

* Journal filters `unmatched`, `matchingStub`, `limit` (including 0) on `GET /__admin/requests`.
* Response shapes: `serveEvents` on `requests/remove`, `requestJournalDisabled` on every journal response,
  `subEvents` on entries, `method: ANY` filled in on stored mappings.
* Endpoints: `requests/remove-by-metadata`, `DELETE mappings/unmatched`, `DELETE requests/{id}` (200 for
  an unknown id), `PUT settings`, `proxyPassThrough` setting, `GET /__admin/docs` (the OpenAPI document and
  a Swagger page, served from the binary with `go:embed`), `GET /__admin/` alias for the mappings list.
* **Strict mappings (FR-10.9).** WireMock answers 422 with a JSON pointer for a field it does not know.
  Today we keep unknown fields (FR-1.4). New setting `mappings.unknownFields: reject | keep`, default
  `reject` in standalone and server mode to match WireMock. This changes FR-1.4 (decision 17).
* Persisted file name `<name>-<id>.json`.

### 5. Wire behaviour (FR-10.10, FR-10.11)

**Status: done.** What the probes of WireMock 3.13.2 changed from the list below:

* `Matched-Stub-Name` is sent only when the mapping has a name. Both headers are in the journal's copy of the reply.
* A request body is inflated before matching, templating and the journal, and the journal keeps the
  `Content-Encoding` header. The inflated size is capped at `limits.maxBodyBytes`.
* CORS is a filter on everything WireMock serves. A preflight (`OPTIONS` with `Origin` and
  `Access-Control-Request-Method`) is answered 200 whether or not a mapping exists and is not journalled; other
  requests get `Vary: Origin`, and `Access-Control-Allow-Origin` (the request's `Origin`, even `null`) and
  `Access-Control-Allow-Credentials: true` when there is an `Origin`. `Access-Control-Allow-Headers` repeats the requested
  headers without spaces. Shadow Mock leaves the admin API out and sends a mapping's own
  `Access-Control-Allow-Origin` alone, where WireMock sends both.
* `/__files/<path>` is served before mappings, and `/<path>` only when no mapping matches. A folder is a 403 and a
  missing file under `/__files/` a 404. Neither is journalled.
* An unreachable proxy target is a 500 whose text begins `Network failure trying to make a proxied request from`.
  `egress_blocked` stays a 502, since WireMock has no such rule.
* A reason phrase is written on the hijacked connection, which is then closed (`Connection: close`); a line break in it
  is replaced by a space. Over HTTP/2 the standard phrase is sent. A body sent in dribbled chunks is not gzipped.
* CI now runs the admin and wire comparisons too, against a third reference server started with `--enable-stub-cors`.

The plan as first written:

* Send `statusMessage` as the reason phrase. Go's server cannot, so replies with a custom phrase are
  written through a hijacked connection, with the same headers and framing as the normal path.
* Send `Matched-Stub-Id` and `Matched-Stub-Name` on every matched reply, as WireMock does. The existing
  `X-Shadow-Mock-Matched` header stays as the opt-in extra. Remove those two names from
  `ignoredHeaders` in the differential test so a regression is caught.
* Gzip: compress any size when the client accepts gzip (the 1 KiB floor goes), and drop `Vary`.
  Decompress request bodies sent with `Content-Encoding: gzip` before matching, templating and the journal.
* CORS: `--enable-stub-cors` / `server.stubCors` adds the `Access-Control-*` headers and answers
  preflight requests the way WireMock does.
* File serving: `GET /__files/<path>` and, when nothing else matches, `GET /<path>` serve files from `__files`.
  Path traversal is refused. `server.serveFiles` can turn it off.
* A proxy target that cannot be reached returns 500 with WireMock's wording instead of 502.
* Kept different on purpose (and listed in COMPATIBILITY.md): `Date` header on replies, `Content-Length`
  instead of chunked framing.

### 6. Command-line flags (FR-10.13)

**Status: done.** What the probes of WireMock 3.13.2 changed from the list below:

* The flag list is checked against WireMock's own `--help` by a test, so a flag WireMock adds fails it.
* `--use-chunked-encoding`, `--timeout`, `--webhook-threadpool-size` and `--max-template-cache-entries` are in the ignored
  group, not the implemented one: Shadow Mock always sends Content-Length, has no global timeout to set, and caches
  nothing that needs a limit. `--timeout` and the `[String]` flags may be given without a value.
* `--filename-template` is a Handlebars template over `id`, `name`, `method` and `url` (also under `request`); the result is
  cleaned like any other file name. A template that does not render stops start-up.
* `--admin-api-basic-auth` guards every admin path (health and version too), and mock traffic is not guarded. Shadow Mock
  adds a `WWW-Authenticate: Basic` header on the 401, which WireMock does not send.
* `--proxy-all` adds a mapping with priority 10, `request.method: ANY` and no URL, as WireMock does. `--record-mappings` records
  what any mapping proxies while it runs, and writes each new request's mapping at once, as WireMock does. A repeat of a
  request already recorded adds nothing (WireMock's behaviour, checked), so the flag has no call-response recording.
* A proxy target refused by `--allow-proxy-targets` or `--deny-proxy-targets` is a 500 with WireMock's wording.
* With no journal, `POST /requests/count` answers `-1` and `requestJournalDisabled: true`, as WireMock does. Listing the journal
  answers an empty list, where WireMock answers a 500.

The plan as first written:

Accept every WireMock flag so a copied command line starts. Each flag is in one of three groups:

* **Implemented:** `--global-response-templating`, `--local-response-templating`,
  `--disable-response-templating`, `--enable-stub-cors`, `--disable-gzip`, `--no-request-journal`,
  `--max-request-journal-entries`, `--bind-address`, `--disable-banner`, `--proxy-all`,
  `--record-mappings`, `--match-headers`, `--preserve-host-header`, `--preserve-user-agent-proxy-header`,
  `--proxy-via`, `--proxy-timeout`, `--allow-proxy-targets`, `--deny-proxy-targets`, `--proxy-pass-through`,
  `--filename-template`, `--permitted-system-keys`, `--logged-response-body-size-limit`,
  `--max-template-cache-entries`, `--admin-api-basic-auth`, `--timeout`, `--webhook-threadpool-size`,
  `--disable-request-logging`, `--print-all-network-traffic`, `--use-chunked-encoding`.
* **Accepted and ignored with one startup warning** (they tune the Java server): `--container-threads`,
  `--jetty-*`, `--async-response-*`, `--max-http-client-connections`, `--disable-connection-reuse`,
  `--disable-extensions-scanning`, `--disable-optimize-xml-factories-loading`,
  `--disable-strict-http-headers`, `--extensions`, `--supported-proxy-encodings`.
* **Implemented in workstream 8:** the TLS and HTTP/2 flags (section 8).
* **Implemented in workstream 9** (until then they stop startup with "not supported yet, see
  docs/PARITY_PLAN.md"): the browser-proxy flags.

Each flag maps onto a configuration key, so the YAML file and environment variables get the same
settings. The `--help` text lists them.

### 7. Recording and snapshots (FR-10.12)

**Status: done.** `TestRecordingMatchesWireMock` snapshots the same traffic on both servers and compares the mappings (request pattern, name, body, headers, order) and the `IDS` output. What the probes changed from the list below:

* A snapshot lists the newest request first. A repeat keeps the last response, not the first. Without `requestBodyPattern` the body is matched by content type (`equalToJson` ignoring array order and extra elements, `equalToXml`, `equalTo` with `caseInsensitive: false`), where Shadow Mock used to record no body pattern.
* `name` is the path with `/` as `_`; a query string becomes `urlPath` plus `queryParameters` with `hasExactly`; every response body is text in `body` (JSON too), `base64Body` for binary; `Date` and `Keep-Alive` stay in the headers.
* `extractBodyCriteria` (`textSizeThreshold`, `binarySizeThreshold`, in `kb`, `mb` or `gb`; a bare `b` is a 422) keeps bodies from that size in `__files` as `<name>-<id>.<ext>`.
* `--record-mappings` has an older shape in WireMock (`url` with the query, no `name`, bodies in `body-<name>-<id>.txt`); Shadow Mock matches it.
* Not done: `repeatsAsScenarios`, because Shadow Mock has no scenarios (`repeatsAsCallResponses` is the replacement).

Recorded mappings match WireMock's shape: `urlPath` plus `queryParameters`, a `name`, text bodies as
`body`, and `outputFormat: IDS` returns `{"ids": [...]}`. `repeatsAsCallResponses` stays. The recorded
mapping shape is covered by a differential test of `snapshot` against both servers.

### 8. TLS and HTTP/2 (FR-10.14)

**Status: done.** `TestTLSMatchesWireMock` runs against a real WireMock 3.13.2 started with `--https-port 18443 --admin-api-require-https` (the fourth server `scripts/wiremock-reference.sh` starts, on :18083). It compares the 403 and its body for the admin API over plain HTTP, scheme and port matching (the case the corpus could not run, `c003000403`) and the `protocol` and `scheme` the journal records for HTTP/1.1, TLS and HTTP/2. What the probes found:

* `--admin-api-require-https` answers plain admin calls 403 with `{"errors":[{"code":40,"title":"HTTPS is required for accessing the admin API"}]}`. Mock traffic is not affected.
* The journal's `protocol` is `HTTP/2.0` for an HTTP/2 request, over TLS or h2c, and an HTTP/2 request has no `Host` header. Shadow Mock always wrote `HTTP/1.1` and added a `Host` header; both are fixed.
* `--disable-http` closes the plain port and not the HTTPS one; `--disable-http2-plain` and `--disable-http2-tls` make an HTTP/2 client fail.
* The keystores are read by `internal/tlsconf` with two new modules, `software.sslmate.com/src/go-pkcs12` and `github.com/pavlo-v-chernykh/keystore-go/v4`. Their formats are tested by writing keystores in the test; there is no keystore in the repository.
* Client certificates, `--disable-http` and the HTTP/2 switches are tested by running the real listeners (`cmd/shadowmock/tls_test.go`), because the acceptance harness serves the engine on test servers.
* Differences kept on purpose are in COMPATIBILITY.md: the generated certificate, a missing truststore refused at start-up, and a separate admin listener staying plain.

What was planned:

* HTTPS listener from PEM files in YAML, and from PKCS12 and JKS keystores through the WireMock flags
  (`--https-port`, `--https-keystore`, `--keystore-type`, `--keystore-password`, `--key-manager-password`).
  Client certificates (`--https-truststore`, `--https-require-client-cert`). `--disable-http`.
  `--admin-api-require-https`.
* HTTP/2: over TLS through ALPN, and in plain text with prior knowledge (`h2c`), each switchable off with
  `--disable-http2-tls` and `--disable-http2-plain`.
* If no keystore is given WireMock generates a self-signed certificate. We do the same.
* **Tests.** Acceptance steps open TLS connections with a test CA; the differential test adds an HTTPS
  listener on the reference WireMock (`--https-port 18443`) and replays the scheme and port cases that are
  skipped today (`c003000403`).

### 9. Browser proxy (FR-10.15)

**Status: done.** `TestBrowserProxyMatchesWireMock` runs against a real WireMock 3.13.2 started with `--enable-browser-proxying --trust-all-proxy-targets` (the fifth server `scripts/wiremock-reference.sh` starts, on :18084). Both servers are sent the same requests through them as a proxy and the answers and the journal are compared. What the probes found:

* A request in absolute form is matched against the mappings by path, whatever host it names, and one that matches nothing goes on to the host it named. This is the same for `CONNECT` tunnels, where the TLS is ended with a certificate made for the host.
* The journal entry has `browserProxyRequest: true`, `scheme`, `host` and `port` of the origin, `absoluteUrl` of the origin, and `wasMatched: true` for a request passed on, so it is not in the unmatched list.
* An origin that cannot be reached is a 500 "Network failure trying to make a proxied request from … to <url>"; one whose certificate is not trusted is a 500 "SSL failure …". Without `--trust-all-proxy-targets` the origin's certificate is checked.
* With the flag off, `CONNECT` is answered 200 and then the connection is of no use; Shadow Mock refuses it with 405.
* Design: `internal/browserproxy` makes the authority (stored, or from a PKCS12 or JKS keystore) and a certificate for each host, bounded to 1024 kept, and serves the tunnel with the router. The unmatched request goes through a second forwarder whose client trusts the origins the settings name. The origin comes from the request and is never put through the template engine.
* Threat review: off unless asked; tunnels are always opened up, never relayed raw, so every request in them passes through the router's allowlist, rate limit and the proxy target rules; a host name must be plain letters, digits, dots, hyphens and underscores; `egress.blockPrivate` applies to the onward call; the authority key stays in a 0600 file (or the keystore the user names); certificates for hosts are kept in memory only. The guide says what is at stake. Left out on purpose: `CONNECT` over HTTP/2, trust options with `proxy.via`.
* Differences kept on purpose are in COMPATIBILITY.md.

What was planned:

`--enable-browser-proxying` with a CA keystore (`--ca-keystore*`), `CONNECT` handling, per-host
certificates signed by that CA, and `--trust-all-proxy-targets` / `--trust-proxy-target`. The journal
entry's `browserProxyRequest` becomes true. This is the largest security surface in the plan, so it
gets its own threat review and is off unless asked for.

### 10. Replacements for common extensions (FR-10.16)

**Status: done, from the extensions' sources.** There is no differential test for this workstream. The three extensions are separate jars that need their dependencies (Datafaker, java-jwt, jose4j, graphql-java) and a build tool to assemble them, and `wiremock-standalone` shades the classes they extend. Instead the SHA-1-checked source jars of wiremock-jwt-extension 0.3.0, wiremock-faker-extension 0.2.0 and wiremock-graphql-extension 0.9.0 were read and the behaviour copied from them:

* **jwt.** The helpers are `jwt`, `claims`, `claimsObject` and `jwks`. Defaults, the reserved claims, the claim types, the array rules, the `maxAge` parser (and its messages, including that units are plural) and `[ERROR: …]` for a mistake are the extension's. Keys live in `templating.jwt` and are generated when absent. The key format WireMock writes ("RSA PRIVATE KEY" around PKCS #8) is read.
* **faker.** `random` evaluates `Class.method` expressions. Datafaker is a Java library with its own data; no Go port has the same names, so a short set of word lists and generators for the common classes is built in, with no new dependency. Both camel and snake case work.
* **graphql.** `graphql-body-matcher` as a custom matcher with parameters `query`, `variables` and `operationName`. A GraphQL reader of our own (no new dependency) compares queries after sorting, and is fuzzed.
* `--extensions` with the Java class of each turns the replacement on; other classes warn.
* Differences kept on purpose are in COMPATIBILITY.md. Left out: gRPC.

What was planned:

Built in, off by default, switched on by `templating.extensions: [jwt, faker]` and
`matching.extensions: [graphql]`:

* `jwt` and `jwks` helpers (signing keys from configuration, claims from hash arguments).
* The Faker `random` helper, using a Go fake-data library with the same `Class.method` names for the
  common classes (`Name`, `Address`, `Internet`, `Company`, `Lorem`, `Number`).
* GraphQL: a `graphql` body matcher that compares queries after normalising whitespace and field order,
  and variables as JSON.

gRPC stays out of scope.

## Definition of done for the whole plan

* Every row of WIREMOCK_GAPS.md is closed, or moved to "Not supported" in COMPATIBILITY.md with a reason.
* The differential test has no skips for behaviour (only for the clock, randomness and addresses).
* `docs/WIREMOCK_GAPS.md` is replaced by a short "verified parity" statement that points to the differential test.
* Release notes for the next minor version list the new flags and the behaviour changes (strict mappings,
  `Matched-Stub-Id`, gzip floor).
