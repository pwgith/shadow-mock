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
* **Implemented in workstreams 8 and 9** (until then they stop startup with "not supported yet, see
  docs/PARITY_PLAN.md"): the TLS, HTTP/2 and browser-proxy flags.

Each flag maps onto a configuration key, so the YAML file and environment variables get the same
settings. The `--help` text lists them.

### 7. Recording and snapshots (FR-10.12)

Recorded mappings match WireMock's shape: `urlPath` plus `queryParameters`, a `name`, text bodies as
`body`, and `outputFormat: IDS` returns `{"ids": [...]}`. `repeatsAsCallResponses` stays. The recorded
mapping shape is covered by a differential test of `snapshot` against both servers.

### 8. TLS and HTTP/2 (FR-10.14)

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

`--enable-browser-proxying` with a CA keystore (`--ca-keystore*`), `CONNECT` handling, per-host
certificates signed by that CA, and `--trust-all-proxy-targets` / `--trust-proxy-target`. The journal
entry's `browserProxyRequest` becomes true. This is the largest security surface in the plan, so it
gets its own threat review and is off unless asked for.

### 10. Replacements for common extensions (FR-10.16)

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
