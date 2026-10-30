# WireMock compatibility

Shadow Mock aims to be a drop-in replacement for WireMock 3.x standalone. This page says how that
is checked, the rule that keeps it true, and every place Shadow Mock deliberately differs.

## How it is checked

Two tests run against a **real WireMock** (3.13.2) on every pull request, in the
`wiremock-reference` CI job. Everything else in the test suite takes its expected results from
Shadow Mock itself. These two take them from WireMock.

1. **Corpus replay** (`test/conformance/differential_test.go`). Every request in
   [corpus/wiremock](../corpus/README.md) is sent to Shadow Mock and to WireMock. The status,
   the headers and the body of each reply are compared, JSON as JSON so layout does not matter.
   Then both request journals are compared: the same requests, matched to the same mappings, with
   the same bodies kept whole. A difference that is not listed below fails the build.
2. **Client suite** (`test/client/compat.test.mjs`). Plain Node code that uses the admin API the
   way a WireMock user's tests do. It must pass against WireMock, which proves each expectation in
   it is what WireMock does, and against Shadow Mock.

To run them yourself you need Java 11 or later:

```bash
bash scripts/wiremock-reference.sh start
SHADOWMOCK_WIREMOCK_URL=http://localhost:18080 go test ./test/conformance -run TestDifferentialAgainstRealWireMock
BASE_URL=http://localhost:18081 node --test test/client/compat.test.mjs
bash scripts/wiremock-reference.sh stop
```

Some corpus cases cannot be compared and are skipped, with the reason in the test: the body holds
the current time or random values, a date is measured from each server's own clock, the case waits
ten seconds on purpose, or the mapping proxies to an upstream that only the acceptance suite starts.

## The rule

**In standalone mode, defaults keep what a WireMock user sees.** A new limit, a stricter check or a
different default may apply by default only in `mode: server`, or when the operator asks for it.
When a change would alter what a WireMock client sees by default, it is listed here, with a test,
before it merges.

This rule exists because a default that cut journal bodies at 64 KiB once got through without being
noticed: it passed every test, and a real client suite found it. See
[the journal](#differences-in-behaviour) below.

## Shadow Mock additions

These are not in WireMock. Mappings that use them do not load in WireMock.

| Addition | What it is |
|---|---|
| Shadow mocks | Key-based and URL-based sessions, `/__admin/sessions/...`, layered resolution |
| `callResponses` | Call-count responses, replacing scenarios |
| Number matchers | `equalToNumber`, `greaterThanNumber`, `greaterThanEqualNumber`, `lessThanNumber`, `lessThanEqualNumber`, also as a `matchesJsonPath` sub-matcher. WireMock 3.13.2 refuses a mapping that uses them |
| `/__admin/mappings/validate`, `dry-run`, `stats/live`, `counters` | Admin endpoints WireMock does not have |
| Request and response `shadowMock` block in journal entries | Which layer and session served a request |
| Custom matchers in Go | `pkg/extension`, instead of Java extensions |
| Built-in `jwt`, `faker` and `graphql` extensions | Stand in for WireMock's separately published extensions; off until `templating.extensions` or `matching.extensions` turns them on, or `--extensions` names the Java class |

## Not supported

| Feature | What happens |
|---|---|
| Scenarios | The mapping is rejected with `scenarios_not_supported`. Use `callResponses` |
| Java extensions | Use Go extensions |

## Differences in behaviour

### Where Shadow Mock is stricter or safer

| Area | WireMock | Shadow Mock | Why |
|---|---|---|---|
| A call to a helper that does not exist | The response is a 500 | The same: a `template_error` 500 | Shadow Mock's template engine used to print nothing, which hid typos |
| Admin tokens (`auth.driver: token`) | No login | Optional bearer tokens with viewer, editor and admin roles; required by `mode: server` | Standalone with no tokens is unchanged. A WireMock client that cannot send an `Authorization` header cannot talk to a server that has tokens |
| Admin changes sent from another web site | Allowed | Refused with 403 when the request has an `Origin` header for another host or a cross-site fetch header | Stops a web page driving an admin API that has no login. Command-line and library clients are not affected |
| XML nested more than 1000 levels | Handled | Not matched, or refused by the `xPath` helper | A body of the maximum size could otherwise stop the server |
| Memory used by the journal | Grows with the journal | Held to `storage.journal.hot.maxBytes` (512 MiB by default); the oldest entries go first | A journal of N entries could otherwise hold N times the largest body |
| A matcher object with no operator, such as `{"equalz": "1"}` | 422 | The same, whatever `mappings.unknownFields` says | It would match nothing, or everything |
| A mapping with a field Shadow Mock does not know, such as a misspelt `urlPaht` | 422 with a JSON pointer | The same, unless `mappings.unknownFields` is `keep`: then the field is stored and returned (FR-1.4). Shadow Mock's own extensions (`callResponses`, `gzipDisabled`) are known | A misspelt field otherwise gives a mapping that matches everything |
| Proxy and webhook targets | Any address | Any address by default. `egress.blockPrivate: true` blocks private, loopback, link-local, carrier-grade NAT and other non-public ranges | For shared servers |

### Where Shadow Mock is more lenient

| Area | WireMock | Shadow Mock |
|---|---|---|
| `{{x}}}` (a tag followed by a closing brace, as in `{"a": {{x}}}`) | A syntax error. Write `{{x}} }` | Accepted |
| A helper given a missing value, for example `{{capitalize request.query.name}}` with no `name` | A 500 (a `NullPointerException`) | Renders an empty string |
| `eq`, `neq` and `contains` on request values, for example `(eq request.method 'GET')` or `(eq request.query.plan 'gold')` | False, because WireMock compares the request-model object with a string. Use `.first`, as in `request.query.plan.first` | True when the text is equal |
| The `else` part of `{{#isOdd}}` and `{{#isEven}}` blocks | Not rendered | Rendered, as for every other block helper |
| A decimal written with a zero fraction in the template, such as `7.0` outside `math` | Keeps the zero (`1.0` in a list) | Prints as a whole number (`1`) |
| Numbers read from JSON | Whole numbers and decimals are kept apart, so `2.0` prints as `2.0` | JSON numbers are doubles, so `2.0` prints as `2` |
| `dateFormat` styles `full`, `long`, `medium`, `short` | The host's locale | US English |
| `parseDate` of text it cannot read | The current time, or nothing | The response fails |
| `unix` and `epoch` dates, and dates with an offset, passed to `date` with `timezone=` | Printed in the host's zone, or as written | Converted to the zone asked for |
| `numberFormat` locales | All of the JDK's | The common ones: en, de, fr, es, it, nl, pt_BR, ja, ko, zh, sv, ru, pl and some regions. Others use English |
| `systemValue type='PROPERTY'` | Reads a Java system property | Prints nothing, because there are none; `ENVIRONMENT` works |
| Negative indexes such as `request.query.x.[-1]` | One before the last value | Not supported; use `[last]` |
| A multipart body with two parts of the same name | The response fails | The first part is read |
| `{{#each request.path}}` | Lists the segments | Also lists the path itself, first |
| `stringFormat '%.1f' 5` | Fails (an int is not a double) | Prints `5.0` |
| A string literal that spans lines | Allowed | Not allowed |
| The `else` part of `{{#contains}}…{{else}}…{{/contains}}` | Not rendered | Rendered, as for every other block helper |
| `this.size` (or another name that is also a Java `Map` property) on an object | The number of entries in the object | The field named `size`, if there is one |

### JSONPath

`matchesJsonPath` follows Jayway JsonPath, as WireMock does. Two expressions that WireMock accepts only by accident are refused: an unclosed bracket (`$.e[`), which WireMock reads as if it were closed.

### Where the bytes differ but the meaning does not

| Area | WireMock | Shadow Mock |
|---|---|---|
| `{{toJson x}}` layout | Pretty printed with Jackson's spacing (`"a" : 1`) | Compact. The JSON is equal |
| The body of a failed response, for example a missing `bodyFileName` | An HTML error page with a stack trace | A small JSON error, `{"error": "not_found", ...}`. Both are status 500 |
| A request that fails because its body file is missing | Not written to the journal | Written to the journal |
| `MALFORMED_RESPONSE_CHUNK` | Sends the headers and no valid body | Sends the headers and a broken chunk. Go's HTTP client fails on Shadow Mock's and not on WireMock's |
| Admin query values that cannot be read: `limit=-1` or `limit=many` on `GET /requests` | 500 | 400 `validation_failed`. A bad `matchingStub` or `since` is a 400 on both |
| The pointer for a misspelt field of a `postServeActions` or `serveEventListeners` entry | The list, `/postServeActions` | The field, `/postServeActions/paramz` |
| The `report` in the `REQUEST_NOT_MATCHED` sub event of an unmatched journal entry | WireMock's side-by-side table | Shadow Mock's own text, the same as the 404 body |
| Swagger UI at `/__admin/swagger-ui/` | Bundled with the server | A page that loads Swagger UI from a CDN (jsDelivr), so it needs internet access. The OpenAPI document at `/__admin/docs/swagger` is served from the binary and describes Shadow Mock's API |
| `Access-Control-Allow-Origin` when stub CORS is on and the mapping sets its own | Both values, the request's origin and the mapping's | The mapping's alone |
| CORS on the admin API | Applied | Not applied: the admin API refuses requests from other web sites (see "Where Shadow Mock is stricter or safer") |
| A reply with a `statusMessage` | Kept alive | Followed by `Connection: close`, because it is written on a connection taken over from Go's server. Over HTTP/2 the standard phrase is sent |
| Files in `__files` | `Last-Modified` and `Accept-Ranges` headers | Not sent |
| `--record-mappings` | Each new request is written as a mapping at once; the body goes to a file `body-<name>-<id>` in `__files` | The same, with Shadow Mock's own file names and ids. A repeat of a request is not written, so `repeatsAsCallResponses` has no effect with the flag |
| Repeats of a request in a snapshot or recording | `repeatsAsScenarios` (on by default) makes a chain of mappings with scenario states; off keeps the last response | Keeps the last response, or with `repeatsAsCallResponses` one mapping with a response for each call. `repeatsAsScenarios` is ignored, because Shadow Mock has no scenarios |
| Recorded mapping files and body files | Named with a random five-character suffix | Named with the mapping's id |
| `transformers` and `transformerParameters` in a snapshot | Accepted | Ignored |
| `--admin-api-basic-auth` | 401 with no `WWW-Authenticate` header | 401 with `WWW-Authenticate: Basic` |
| Listing the journal when it is off | 500 `RequestJournalDisabledException` | 200 with an empty list and `requestJournalDisabled: true` (a count is `-1` on both) |
| WireMock flags that tune the Java server (`--jetty-*`, `--container-threads` and the like) | Take effect | Accepted, with a warning at start-up, and ignored |
| The certificate when no keystore is given | WireMock's own built-in keystore, the same on every start | A self-signed ECDSA certificate for `localhost`, `127.0.0.1` and `::1`, made on each start. A client that pins WireMock's certificate must be given the new one, or told not to verify |
| `--keystore-type` | `JKS` unless told otherwise | `JKS` or `PKCS12`. With no type a file is read as JKS if it is one, and as PKCS12 if not |
| `--https-require-client-cert` with no `--https-truststore` | Starts, and then no client can connect | Refused at start-up, naming the missing truststore |
| The admin listener of `listen.admin` | (WireMock has no second listener) | Plain HTTP. HTTPS serves what the mock listener serves |
| A TLS handshake that fails | Logged at debug | Logged by Go's HTTP server on standard error |
| The certificate authority when `--ca-keystore` is not given | One kept in `~/.wiremock/ca-keystore.jks` | One made on first use, stored in `~/.shadowmock` (or `proxy.browser.caDir`) with `ca.pem` beside it. Its SHA-256 is logged at start-up |
| The JWT keys | In the extended global settings, made on first start and kept with them | In `templating.jwt`; what is not set is made at each start, so tokens do not outlive the server. `POST /__admin/settings` does not carry them |
| A date other than `exp` and `nbf` given to `{{jwt}}` as a claim | A date claim, in seconds | The text of the date. The `now` helper gives text here, not a date |
| The `random` helper's values | Datafaker's, in every locale | Shadow Mock's own short word lists, in English, for `Name`, `Address`, `Internet`, `Company`, `Lorem`, `Number`, `PhoneNumber`, `Bool` and `Color`; another class or method is an `[ERROR: …]` |
| The graphql matcher's reading of a query | graphql-java's | Executable documents only (operations and fragments); a schema definition is a syntax error and so no match. Selections are compared in sorted order, as the extension sorts them |
| `CONNECT` when browser proxying is off | Answers 200, and then the connection is of no use | 405, naming the setting |
| `CONNECT` over HTTP/2, and `CONNECT` to a host name with characters other than letters, digits, dots, hyphens and underscores | Served | Refused (501 and 400) |
| Browser proxying and `--proxy-via` with `--trust-all-proxy-targets` or `--trust-proxy-target` | Accepted | Refused at start-up: the trust is applied to connections Shadow Mock makes itself |
| `--trust-proxy-target` | An origin (host) | A host name or IP address; a port is not part of it |
| Body text of the 500 for a failed pass-through | "…from WireMock to …" | "…from Shadow Mock to …"; the rest is the same, and an untrusted certificate is "SSL failure" on both |
| Journal `clientIp` | Written as `[0:0:0:0:0:0:0:1]` for IPv6 | Written as the plain address |

### Behaviour that matches WireMock on purpose

These were once different and were changed to match what WireMock does. They are listed so they
are not "fixed" again.

- A stub that gives no `Content-Type` gets none, including for `jsonBody`. Go would otherwise guess
  one from the body.
- The `Host` header is in `request.headers`, in the journal and available to header matchers.
- The journal keeps request and response bodies whole, and each entry has `bodyAsBase64`,
  `protocol`, `scheme`, `host`, `port`, `formParams` and `browserProxyRequest`.
- A single query parameter, header or cookie value can be reached as `.first`, `.last` and `.[0]`,
  and `toJson` writes it as a string.
- `toJson request.headers` lists each header once.
- Helpers that WireMock has and Shadow Mock lacked: `matches` (block and expression), `val`,
  `arrayAdd`, `arrayJoin`, `range`, `date`, `pickRandom`, `randomDecimal`, `soapXPath` and
  `formData`.

## When you find a difference

If a mapping or a client behaves differently on Shadow Mock, that is a bug unless it is on this
page. The most useful report is the request and both responses. The best fix adds the case to the
corpus or to `test/client/compat.test.mjs`, so WireMock confirms what the right answer is.
