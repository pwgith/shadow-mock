# WireMock gaps

Found on 2026-10-28 by comparing Shadow Mock with WireMock's documentation, its test suite (344 test
classes on `master`) and a real WireMock 3.13.2. "Verified" means the same request was sent to both
servers and the answers differed. "Docs" means the WireMock documentation says it works and it has not
been run. Scenarios are left out on purpose.

## 1. Command-line flags (closed by workstream 6, FR-10.13, except the TLS, HTTP/2 and browser-proxy flags)

All the flags below are now accepted. The ones in the TLS, HTTP/2 and browser-proxy rows are refused with "not supported yet" until workstreams 8 and 9. What each flag does is in [the configuration guide](guide/11-configuration.md).

What it was:

Shadow Mock accepts `--port`, `--root-dir`, `--verbose`, `--version`, `--help` and its own flags. Every
other WireMock flag stops the server at startup with "flag provided but not defined". A command line
or Docker `command:` copied from a WireMock setup will not start.

| Group | Flags |
|---|---|
| Common | `--global-response-templating`, `--local-response-templating`, `--disable-response-templating`, `--enable-stub-cors`, `--disable-gzip`, `--no-request-journal`, `--max-request-journal-entries`, `--bind-address`, `--disable-banner`, `--disable-request-logging` |
| Proxy and record | `--proxy-all`, `--record-mappings`, `--match-headers`, `--preserve-host-header`, `--preserve-user-agent-proxy-header`, `--proxy-via`, `--proxy-timeout`, `--allow-proxy-targets`, `--deny-proxy-targets`, `--proxy-pass-through`, `--supported-proxy-encodings`, `--filename-template` |
| TLS and HTTP/2 | `--https-port`, `--disable-http`, `--https-keystore`, `--keystore-type`, `--keystore-password`, `--key-manager-password`, `--https-truststore`, `--truststore-type`, `--truststore-password`, `--https-require-client-cert`, `--disable-http2-plain`, `--disable-http2-tls`, `--admin-api-require-https` |
| Admin | `--admin-api-basic-auth` |
| Browser proxy | `--enable-browser-proxying`, `--ca-keystore*`, `--trust-all-proxy-targets`, `--trust-proxy-target` |
| Tuning (can be accepted and ignored) | `--container-threads`, `--jetty-*`, `--async-response-enabled`, `--async-response-threads`, `--max-http-client-connections`, `--max-template-cache-entries`, `--use-chunked-encoding`, `--disable-connection-reuse`, `--disable-extensions-scanning`, `--disable-optimize-xml-factories-loading`, `--disable-strict-http-headers`, `--permitted-system-keys`, `--logged-response-body-size-limit`, `--print-all-network-traffic`, `--timeout`, `--webhook-threadpool-size`, `--extensions` |

## 2. JSONPath dialect (verified) — closed by workstream 1 (FR-10.1, FR-10.2)

WireMock uses the Jayway JsonPath library. Shadow Mock uses a different library, so these `matchesJsonPath`
expressions fail. Most are refused when the mapping is loaded.

| Expression | Shadow Mock | WireMock |
|---|---|---|
| `$[?(@.things.size() == 2)]` (the example in WireMock's own docs) | mapping refused | matches |
| `$.a.size()` as a path | mapping refused | matches |
| `$[?(@.a =~ /ab.*/)]`, `=~ /x/i` | no match, or refused | matches |
| `$[?(@.a in ['x','y'])]`, `nin`, `subsetof`, `anyof`, `noneof`, `contains`, `size`, `empty` | no match, or refused | match |
| `$[?(@.a == 'x')]`, `> 1`, `&&`, `||`, `!`, `== true`, `== null`, `@['a'] == 1` when `$` is an object | no match | matches |
| `$.a.min()`, `max()`, `sum()`, `keys()` | mapping refused | accepted |

The corpus only filters on child arrays (`$.items[?(...)]`), which both servers handle, so this was not caught.

Also: JsonUnit's `${json-unit.ignore-element}` in `equalToJson` does not match in Shadow Mock.

## 3. Response templating (verified)

### Block forms that return nothing or an error (closed by workstream 2, FR-10.3)

`{{#assign 'x'}}...{{/assign}}` assigns nothing, so every example that builds on it (`jsonArrayAdd`,
`jsonMerge`, `jsonRemove`, `jsonSort`, `formatJson`) breaks. Also broken: `{{#parseJson 'x'}}...{{/parseJson}}`,
`{{#formatJson}}`, `{{#formatXml}}`, `{{#contains a b}}Y{{/contains}}` (prints `true`), `{{#trim}}`,
`{{val 10 assign='v'}}{{#lt v 20}}`, `{{jsonArrayAdd}}` and `{{jsonMerge}}` with an assigned variable.

### Helpers that do not exist (closed by workstream 3, FR-10.4; `jsonSort` is 4.x only and stays out)

`numberFormat`, `arrayRemove`, `isOdd`, `isEven`, `stripes`, `systemValue`, `formatXml`, and the string
helpers `abbreviate`, `capitalizeFirst`, `center`, `cut`, `defaultIfEmpty`, `ljust`, `rjust`, `slugify`,
`stringFormat`, `wordWrap`, `yesno`, `stripTags`, `dateFormat`. WireMock also ships `jsonSort` (4.x beta only).

### Helpers that behave differently (closed by workstream 3, FR-10.5)

The probe of WireMock 3.13.2 for workstream 3 corrected this table: `parseDate` does not read RFC 1036 dates and
only some `truncateDate` names exist (the list is in the user guide), `math` divides at the scale of the first
number (`6 / 4` is `2`, `7.0 / 2` is `3.5`), and negative indexes are off by one, so they are not copied.

| Helper | Shadow Mock | WireMock |
|---|---|---|
| `{{hostname}}` | empty | the machine name |
| `{{array 1 'a'}}`, `{{range 3 5}}`, `{{arrayAdd ...}}` printed directly | `1a`, `345` | `[1, a]`, `[3, 4, 5]` |
| `{{arrayAdd (array 1 'three') 2 position=1}}` | appends | inserts at the position |
| `{{arrayJoin ',' list prefix='[' suffix=']'}}` | ignores prefix and suffix | applies them |
| `{{math 6 '/' 4}}` | `1.5` | `2` (integer division) |
| `{{jsonPath '{"a":[1,2]}' '$.a'}}` | `12` | `[1,2]` |
| `{{#xPath xml '/a/b'}}{{this}}{{/xPath}}` | the text `1` | the element `<b>1</b>` |
| `{{urlEncode 'a%20b' decode=true}}` | ignores `decode` | `a b` |
| `{{parseDate 'Tue, 15 Jun 2021 15:16:17 GMT'}}` | error | parsed (RFC 1123, RFC 1036, asctime) |
| `{{parseDate '1623770177' format='unix'}}` and `format='epoch'` | error | parsed |
| `{{truncateDate x 'first day of next month'}}`, `'first second of minute'` | error | works |
| `{{lookup request.query 'ids[].1'}}`, `request.query.x.[-1]` | empty, error | `222`, last value |

### Request model (closed by workstream 3, FR-10.6)

Missing: `request.id`, `request.path.N` (the path as segments), `request.requestLine.*`, and for multipart
requests `request.multipart`, `request.parts.<name>.body`, `.bodyAsBase64`, `.binary`, `.headers.<key>`.

## 4. Admin API (closed by workstream 4, FR-10.7 to FR-10.9)

Every row below was checked again against WireMock 3.13.2 for workstream 4, and the admin API is now compared with a real WireMock by `TestAdminAPIMatchesWireMock`. The deliberate differences that remain (bad query values, error bodies, Swagger UI from a CDN) are in [COMPATIBILITY.md](COMPATIBILITY.md).

| Endpoint or behaviour | Shadow Mock | WireMock |
|---|---|---|
| `GET /__admin/requests?unmatched=true` | ignored (all requests) | only unmatched |
| `GET /__admin/requests?matchingStub=<id>` | ignored | only that stub's requests |
| `GET /__admin/requests?limit=0` | 1 entry | 0 entries |
| `POST /__admin/requests/remove` | `{"requests":[]}` | `{"serveEvents":[]}` |
| `POST /__admin/requests/remove-by-metadata` | 404 | supported |
| `requestJournalDisabled` in `/requests`, `/requests/find`, `/requests/count`, `/requests/unmatched` | missing in all but `/requests` | present |
| `DELETE /__admin/mappings/unmatched` | 404 | supported |
| `DELETE /__admin/requests/{id}` for an unknown id | 404 | 200 |
| `PUT /__admin/settings`, `proxyPassThrough` in settings | missing | supported |
| `GET /__admin/docs`, `/__admin/docs/swagger`, `/__admin/swagger-ui/` | 404 | Swagger UI and OpenAPI document |
| `GET /__admin/` | 404 | the mappings list |
| `POST /__admin/recordings/snapshot` with `outputFormat: IDS` | returns mappings | returns `{"ids":[...]}` |
| A mapping with a misspelt field (`{"request":{"urlPaht":"/a"}}`, top level, `request` or `response`) | accepted, field ignored | 422 with a JSON pointer |
| Stored mapping | `request.method` absent when not given | `"method": "ANY"` filled in |
| Journal entry | has `shadowMock` | also has `subEvents` |
| Persisted mapping file | `<id>.json` | `<name>-<id>.json` |

## 5. Wire behaviour (closed by workstream 5, FR-10.10 and FR-10.11, except the rows marked later)

Rows for HTTP/2, HTTPS and the browser proxy belong to workstreams 8 and 9. `TestWireBehaviourMatchesWireMock` compares the rest with a real WireMock; the deliberate differences are in [COMPATIBILITY.md](COMPATIBILITY.md).

| Behaviour | Shadow Mock | WireMock |
|---|---|---|
| `response.statusMessage` | ignored (reason phrase is the standard one) | sent |
| `Matched-Stub-Id` response header on a matched request | not sent (off by default, and it is `X-Shadow-Mock-Matched`) | always sent. The differential test ignores this header, so it was never noticed |
| Gzip of responses | only bodies of 1 KiB or more, and adds `Vary` | any size, no `Vary` |
| Request body with `Content-Encoding: gzip` | not decompressed for matching (404) | decompressed and matched |
| CORS (`--enable-stub-cors`): preflight and `Access-Control-*` headers | none | supported |
| Files in `__files` served over HTTP at `/__files/<path>` and at `/<path>` | 404 | served |
| Proxy target unreachable | 502 `bad_gateway` | 500 |
| HTTP/2 over plain text (h2c, prior knowledge) | refused | served |
| HTTPS, client certificates, browser proxy | not built | supported (docs) |
| `Date` header on responses | sent | not sent |
| Content-Length vs chunked | always Content-Length | chunked for most responses |
| Recorded mapping shape | `url` with the query string, body as `jsonBody`, no `name` | `urlPath` plus `queryParameters`, body as text, `name`, `Date` header kept |

## 6. Beyond core WireMock (docs)

* Extensions published separately: JWT helpers (`jwt`, `jwks`), GraphQL matching, Faker helpers (`random`), gRPC.
* WireMock 4.x beta only: messaging (`/__admin/message-mappings`, `/messages`, `/channels`), WebSockets,
  Server-Sent Events, the `proxied-hostname-rewrite` transformer, `jsonSort`.
* Java-only features: JUnit and Spring integrations, WAR deployment, Java extension points, custom matchers by class.

## 7. Parts of the WireMock test suite with no counterpart here

| Area (WireMock test classes) | Notes |
|---|---|
| HTTPS and certificates (`HttpsAcceptanceTest`, `ClientAuthenticationAcceptanceTest`, `CertificateAuthorityTest`, `KeyStoreSettingsTest`) | TLS not built |
| Browser proxy (`BrowserProxyAcceptanceTest`, `HttpsBrowserProxy*`) | not built |
| HTTP/2 (`Http2AcceptanceTest`, `Http2DisabledAcceptanceTest`) | gap in section 5 |
| Command line (`CommandLineOptionsTest`, `WireMockConfigurationTest`, `BindAddressTest`) | gap in section 1 |
| Template helpers (`*HelperTest`, `ResponseTemplatingAcceptanceTest`, `MultipartTemplatingAcceptanceTest`, `ArrayFunctionsTest`, `ListFunctionsTest`, `JsonMergeHelperTest`, `JsonSortHelperTest`) | gap in section 3 |
| JSON path and schema matching (`MatchesJsonPathPatternTest`, `JsonSchemaMatchingAcceptanceTest`) | gap in section 2 |
| Serve-event log and journal (`ServeEventLogAcceptanceTest`, `SubServeEventsAcceptanceTest`, `LoggedRequestTest`) | gap in section 4 |
| File serving (`SingleRootFileSourceTest`, `ClasspathFileSourceTest`, `FilenameMakerTest`) | gap in sections 4 and 5 |
| CORS (`CrossOriginTest`), transfer encoding (`TransferEncodingAcceptanceTest`), gzip (`GzipAcceptanceTest`) | gaps in section 5 |
| Messaging, WebSockets, SSE (`Websocket*`, `Message*`, `SseAcceptanceTest`) | 4.x beta |
| Extensions API (`ExtensionsTest`, `*TransformerAcceptanceTest`, `RequestFilter*`, `StubLifecycleListener*`) | Java only |
| JUnit, Spring, WAR (`JUnitJupiter*`, `WarDeployment*`) | Java only |

Already well covered by the corpus and the differential test: URL, header, query, cookie, form and
body matching (JSON, XML, XPath, schema, multipart), dates, logical operators, priority, delays and
faults, proxying basics, webhooks, near misses, import and metadata calls, settings.

## How these were found

The probe scripts (matching, JSONPath, templating, admin API, wire behaviour, file loading, proxying,
doc examples) send the same requests to Shadow Mock and to WireMock 3.13.2 and print the differences.
They are not in the repository. Turning them into corpus cases would make each gap a failing test
before it is fixed.
