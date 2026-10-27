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

## Not supported

| Feature | What happens |
|---|---|
| Scenarios | The mapping is rejected with `scenarios_not_supported`. Use `callResponses` |
| Global browser-proxy mode | Not built |
| Java extensions | Use Go extensions |

## Differences in behaviour

### Where Shadow Mock is stricter or safer

| Area | WireMock | Shadow Mock | Why |
|---|---|---|---|
| A call to a helper that does not exist | The response is a 500 | The same: a `template_error` 500 | Shadow Mock's template engine used to print nothing, which hid typos |
| Admin changes sent from another web site | Allowed | Refused with 403 when the request has an `Origin` header for another host or a cross-site fetch header | Stops a web page driving an admin API that has no login. Command-line and library clients are not affected |
| XML nested more than 1000 levels | Handled | Not matched, or refused by the `xPath` helper | A body of the maximum size could otherwise stop the server |
| Memory used by the journal | Grows with the journal | Held to `storage.journal.hot.maxBytes` (512 MiB by default); the oldest entries go first | A journal of N entries could otherwise hold N times the largest body |
| Proxy and webhook targets | Any address | Any address by default. `egress.blockPrivate: true` blocks private, loopback, link-local, carrier-grade NAT and other non-public ranges | For shared servers |

### Where Shadow Mock is more lenient

| Area | WireMock | Shadow Mock |
|---|---|---|
| `{{x}}}` (a tag followed by a closing brace, as in `{"a": {{x}}}`) | A syntax error. Write `{{x}} }` | Accepted |
| A helper given a missing value, for example `{{capitalize request.query.name}}` with no `name` | A 500 (a `NullPointerException`) | Renders an empty string |
| `eq`, `neq` and `contains` on request values, for example `(eq request.method 'GET')` or `(eq request.query.plan 'gold')` | False, because WireMock compares the request-model object with a string. Use `.first`, as in `request.query.plan.first` | True when the text is equal |
| `this.size` (or another name that is also a Java `Map` property) on an object | The number of entries in the object | The field named `size`, if there is one |

### Where the bytes differ but the meaning does not

| Area | WireMock | Shadow Mock |
|---|---|---|
| `{{toJson x}}` and `{{formatJson x}}` layout | Pretty printed with Jackson's spacing (`"a" : 1`) | Compact. The JSON is equal |
| The body of a failed response, for example a missing `bodyFileName` | An HTML error page with a stack trace | A small JSON error, `{"error": "not_found", ...}`. Both are status 500 |
| A request that fails because its body file is missing | Not written to the journal | Written to the journal |
| `MALFORMED_RESPONSE_CHUNK` | Sends the headers and no valid body | Sends the headers and a broken chunk. Go's HTTP client fails on Shadow Mock's and not on WireMock's |
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
