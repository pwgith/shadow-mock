# Mapping corpus

WireMock-style mapping and response files that exercise every supported feature, in two trees:

* **`wiremock/`**: everything WireMock 3.x does **except scenarios** (not supported, FR-4.4). This tree is a standard WireMock root (`mappings/` + `__files/`), so it loads unchanged in **both Shadow Mock and real WireMock**. That makes it the basis for the differential conformance run in `features/06_non_functional/compatibility.feature`.
* **`shadow-mock/`**: features unique to Shadow Mock: call-count responses, gzip control, key-based and URL-based shadow mocks (sessions, tokens, overrides, masks, nesting, seeding, session files), counters, runtime endpoints, and inputs that must be rejected.

The examples use claims, payments and policies as a realistic domain. Every mapping has a fixed `id`, a descriptive `name` and `metadata.corpus`, so tests can find, verify and remove them by id or metadata.

## Layout

```
corpus/
├── wiremock/                         WireMock root: load with --root-dir corpus/wiremock
│   ├── mappings/
│   │   ├── 01-url-matching/          url, urlPath, urlPattern, urlPathPattern, urlPathTemplate + pathParameters, case, encoding
│   │   ├── 02-methods/               every HTTP method, ANY, no method
│   │   ├── 03-request-attributes/    headers (incl. multi-value), query (hasExactly, includes, absent),
│   │   │                             cookies, formParameters, basic auth, host, port, scheme
│   │   ├── 04-value-matchers/        string operators, and/or/not, date-time (absolute, relative,
│   │   │                             actualFormat, truncation), number matchers
│   │   ├── 05-body-json/             equalToJson (+flags, string form, json-unit placeholders),
│   │   │                             matchesJsonPath (presence, filters, sub-matchers), matchesJsonSchema
│   │   ├── 06-body-xml/              equalToXml (+placeholders, delimiters, exempted comparisons),
│   │   │                             matchesXPath (+sub-matcher, namespaces, SOAP)
│   │   ├── 07-body-other/            text bodies, empty body, binaryEqualTo, multipart (ANY, ALL, named parts)
│   │   ├── 08-priority/              catch-all vs specific, default priority 5
│   │   ├── 09-responses/             status, statusMessage, headers (multi-value), body, jsonBody,
│   │   │                             base64Body, bodyFileName (json, xml, text, csv, missing), 204, 301, 500
│   │   ├── 10-templating/            full request model, path parameters, transformerParameters,
│   │   │                             templated bodyFileName, headers and file contents; date, random,
│   │   │                             math, string, regex, logic, array and form helpers; JSON and XML helpers,
│   │   │                         inline partials, block parameters and per-field defaults (a template file
│   │   │                         in the style of real WireMock setups, whose full output the conformance test checks)
│   │   ├── 11-delays/                fixed, uniform, lognormal (+maxValue), chunked dribble, timeout
│   │   ├── 12-faults/                all four fault types
│   │   ├── 13-proxying/              proxyBaseUrl, partial mocking, header add/remove,
│   │   │                             prefix removal, templated target
│   │   ├── 14-webhooks/              templated body, fixed and random delay, several webhooks,
│   │   │                             legacy postServeActions
│   │   └── 15-metadata-and-misc/     metadata for find/remove, near-miss target, persistent mapping
│   ├── __files/                      claims/, policies/, text/, exports/, soap/, templates/
│   ├── extensions/                   custom matcher (load only with the is-even-id extension registered)
│   └── admin/                        payloads for the admin API: import, find/remove by metadata,
│                                     verification patterns, near misses, record and snapshot specs, settings
├── shadow-mock/                      Shadow Mock only: load with root corpus/shadow-mock
│   ├── mappings/
│   │   ├── 01-call-count-responses/  retry, polling, every selector form, rule order, rate limit, flaky,
│   │   │                             {{request.callNumber}}, rich rule responses, counter admin target
│   │   ├── 02-gzip/                  compressed by default, gzipDisabled, small and pre-encoded bodies
│   │   └── 03-session-baseline/      global mappings the session examples override, mask or fall through to
│   ├── __files/                      large/ (12 KB JSON for gzip), session/ (file a session overrides)
│   ├── mapping-sets/                 named set "declined-payments" for seedFrom
│   ├── sessions/                     admin API payloads:
│   │   ├── key-based/                create (minimal, tokens + mappings, override by id, masks, seeded),
│   │   │                             a session mapping with callResponses, PATCH payloads
│   │   ├── url-based/                create (minimal, 30-day CI mock with call counts), nested key-based
│   │   ├── counters/                 PUT body for setting a counter
│   │   ├── files/                    session file override for session/claim-from-file.json
│   │   ├── recording/                record into a session with repeatsAsCallResponses
│   │   └── runtime/                  dry-run and validate payloads
│   └── negative/                     must be rejected: scenario fields, invalid call rules,
│                                     invalid mappings, invalid session payloads (each with _expect)
├── wiremock.http                     runnable requests for the wiremock tree (VS Code REST Client)
└── shadow-mock.http                  runnable session and call-count flows for the shadow-mock tree
```

## Running

**Shadow Mock (standalone):** point the engine at a root.

```bash
shadowmock --root corpus/wiremock
```

**Real WireMock (for comparison):**

```bash
docker run --rm -p 8080:8080 -v "$PWD/corpus/wiremock:/home/wiremock" wiremock/wiremock:3.13.1 --global-response-templating=false
```

**Requests:** open `corpus/wiremock.http` or `corpus/shadow-mock.http` in VS Code with the REST Client extension (`humao.rest-client`) and click **Send Request** above each one. Each request states the expected result in a comment.

## External test doubles

Some mappings point at local helpers. Start them when you exercise those folders:

| Port | Helper | Used by |
|---|---|---|
| 8089 | Upstream stub server (any HTTP server; WireMock or Shadow Mock on another port works) | `13-proxying`, `admin/recording`, `sessions/recording` |
| 8090 | Webhook receiver (any server that logs requests) | `14-webhooks` |
| 8443 | HTTPS listener on the mock itself | `03-request-attributes/host-port-scheme.json` (scheme/port mapping) |

## Conventions

* **Ids:** `c0NNxxxx-…` for the WireMock tree (NN = folder number) and `5d0Nxxxx-…` for the Shadow Mock tree. Ids are unique across the corpus. `scripts/check-corpus.js` verifies this.
* **Paths:** `/wm/<area>/…` in the WireMock tree and `/sm/<area>/…` in the Shadow Mock tree, so the trees can be loaded together without clashing.
* **Placeholders:** `${URL_MOCK_ID}` and `${SESSION_ID}` in session payloads are filled in by the test, or by the `.http` files, from earlier responses.
* **Negative files:** each has an `_expect` block describing the required status and error code. They are not mappings and live outside `mappings/`, so they are never loaded automatically.
* **Templated files** in `__files/templates/` are Handlebars templates, not valid JSON until rendered. The corpus check skips them.
