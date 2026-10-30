# Shadow Mock: Executable Specifications

Specification by example, written in Gherkin, for running with Cucumber. The Go engine uses [godog](https://github.com/cucumber/godog). The management-app features can also run with Cucumber.js against the Next.js app.

Every requirement in [REQUIREMENTS.md](../docs/REQUIREMENTS.md) is covered by at least one scenario. Features are tagged with the requirement IDs they cover. See the coverage matrix in [COVERAGE.md](COVERAGE.md).

## Layout

```
features/
├── 01_wiremock_parity/        WireMock-compatible behaviour of the global session (both modes)
│   ├── mappings/              loading, admin CRUD, priority
│   ├── request_matching/      URL, method, headers/query/cookies, value matchers, bodies, auth, custom
│   ├── responses/             definitions, templating, delays, faults, proxying, gzip, unmatched
│   ├── journal/               request journal, verification, near misses
│   ├── record_playback/       recording and snapshots
│   ├── webhooks/              serve-event listeners
│   └── admin/                 files, settings, reset, health, shutdown
├── 02_shadow_mocks/           Shadow Mock's differentiators (both modes)
│   ├── key_based/             lifecycle, detection, tokens, ambiguity, layering, isolation, scoped admin
│   ├── url_based/             lifecycle, routing, resolution, nesting
│   └── common/                seeding, session files, recording into sessions
├── 03_standalone/             standalone and central-server specifics
├── 04_saas/                   SaaS engine behaviour
│   ├── tenancy/               org handle routing, aliases, isolation
│   ├── auth/                  trusted issuer tokens, Tool app keys, metadata scope, org match, roles, global-write restriction
│   ├── network/               IP allowlist, client IP
│   ├── data_sync/             change propagation, lazy loading
│   ├── journal/               hot and warm tiers, object storage, retention
│   ├── plans_and_limits/      entitlements, rate and session limits, metering
│   └── operations/            health, shutdown, SSRF, managed-mode guards, shared state, object storage, degraded operation, audit and purge
├── 05_management_app/         what the management app needs from the engine's admin API (the app's own behaviour is specified in the platform repository)
├── 06_non_functional/         performance, security, compatibility
└── 07_stateful_responses/     call-count responses (replace WireMock scenarios); scenario rejection
```

## Modes and tags

| Tag | Meaning |
|---|---|
| *(no mode tag)* | Runs in **both** modes. The harness runs it once as `@standalone` and once as `@saas` |
| `@standalone` | Standalone or central-server mode only |
| `@saas` | SaaS mode only |
| `@central-server` | Standalone binary deployed as a shared server |
| `@management-app` | Exercised through the management app (UI or its API), not the engine directly |
| `@wiremock` | WireMock parity behaviour |
| `@key-based`, `@url-based` | Shadow mock type under test |
| `@multi-instance` | Needs two or more engine instances |
| `@nfr`, `@perf`, `@security` | Non-functional. `@perf` runs in the benchmark pipeline, not on every PR |
| `@P0` … `@P5` | Delivery phase (DESIGN.md §15). CI runs all phases up to the current one |
| `@FR-x.y`, `@NFR-x.y` | Requirement traceability |
| `@wip` | Not yet implemented, excluded by default |

## Base URLs

Steps never hard-code hosts. The harness resolves them per mode:

| Name in steps | Standalone | SaaS |
|---|---|---|
| **mock base** | `http://localhost:8080` | `https://shadowmock.test/<handle>` of the current org (default org `acme`) |
| **admin base** | `{mock base}/__admin` | `{mock base}/__admin` |
| **URL-based base** | `{mock base}/d/<id>` | `{mock base}/d/<id>` |

"I send a GET request to `/claims/1`" means `GET {mock base}/claims/1`.

## Step conventions

Shared vocabulary so step definitions are reused. Parameters in `"quotes"`, payloads in doc strings, tabular data in tables.

```gherkin
# Environment
Given Shadow Mock is running
Given Shadow Mock is running with configuration:          # YAML doc string, merged over defaults
Given Shadow Mock is running with 2 instances               # @multi-instance
Given the org "acme" with handle "acme" on plan "Team"     # @saas
Given the test clock is at "2026-11-01T09:00:00Z"
Given the test clock advances by "2h"

# Identity (@saas; standalone always authenticates as local/local/admin)
Given I am "alice" in org "acme" with role "org:member"
Given I am CI key "ck_ABC" in org "acme"
Given I am not authenticated
Given I present a JWT with:                                 # table of claims to craft edge cases

# Global mappings
Given the global mapping:                                   # JSON doc string (WireMock StubMapping)
Given the global mappings file "mappings/x.json":           # standalone fs store
Given the file "__files/body.json":

# Sessions
Given a key-based shadow mock "S1"                          # stores ${S1.id} and ${S1.key}
Given a key-based shadow mock "S1" with:                    # JSON create payload
Given a URL-based shadow mock "U1"                          # stores ${U1.id} and ${U1.baseUrl}
Given the mapping in "S1":                                  # session-scoped mapping

# Requests to mocks
When I send a "GET" request to "/path"
When I send a "POST" request to "/path" with body:
When I send a request:                                      # table: method, path, header:<name>, query:<name>, cookie:<name>, body
When I send a "GET" request to "/path" via "U1"             # through the URL-based base
When I send a "GET" request to "/path" from IP "203.0.113.7"

# Admin API
When I call admin "POST" "/mappings" with:
When I call admin "GET" "/sessions/${S1.id}/requests"

# Assertions
Then the response status is 200
Then the response body is:
Then the response body is "text"
Then the response JSON at "$.a.b" is "x"
Then the response JSON at "$.items" has 3 items
Then the response header "Content-Type" is "application/json"
Then the response header "X-Foo" is absent
Then the response arrives after at least 500 ms
Then the connection is reset

# Variables
And I remember the response JSON at "$.id" as "ID"         # use later as ${ID}
```

`${NAME}` is used for variables so it never clashes with Handlebars `{{…}}` in templating specs.

## Test doubles provided by the harness

| Double | Used by |
|---|---|
| **Fake token issuer** (key set endpoint; mints management-app-style tokens with arbitrary claims and lifetimes, and Tool app keys) | `04_saas/auth`, `05_management_app` |
| **Test clock** (injected `clock.Clock`) | TTLs, retention, counter expiry, templating `now` |
| **Upstream stub server** (records requests, configurable responses) | Proxying, recording |
| **Webhook receiver** | Webhooks |
| **In-memory S3** (or MinIO) | Object storage |
| **Postgres + Redis containers** (testcontainers) | `@saas` and `@multi-instance` |
| **Proxy simulator** (sets `X-Forwarded-For` like Render) | IP allowlist and client IP |
| **WireMock reference container** | Optional differential run of `01_wiremock_parity` (`@wiremock`) |

## Harness semantics

* **`Given the global mapping`** seeds the org's global session through the store: the file system or admin API in standalone, and the database (as the management app would) in SaaS. It is not an engine admin call, so it works in SaaS, where engine global writes are rejected.
* **`Given the mapping in "S1"`** uses the session-scoped admin API with the current identity.
* **Identity:** in `@saas` runs where no identity is given, the harness acts as an `org:member` of `acme`, using a Clerk session JWT from the fake issuer.
* **Upstream stub servers and webhook receivers** are added to the SaaS egress allowlist for the run, so proxying and webhook features work in both modes. `04_saas/operations/egress_policy.feature` tests the policy itself.
* **Timing assertions** ("within 1 s", "after at least 500 ms") use real time, apart from TTL and retention steps, which use the test clock.

## API details these specs pin down

Writing the examples surfaced these concrete API details. All of them are now defined in [api/openapi.yaml](../api/openapi.yaml):

| Detail | Where specified |
|---|---|
| `GET /__metrics` (Prometheus) | `03_standalone/central_server.feature` |
| `PUT/GET/DELETE /__admin/sessions/{id}/files/{name}` | `02_shadow_mocks/common/session_files.feature` |
| `POST /__admin/requests/remove` (WireMock remove-by-pattern) | `01_wiremock_parity/journal/verification.feature` |
| `seedFrom: { mappingSet }` on session create, and saved mapping sets | `02_shadow_mocks/common/seeding.feature` |
| `PATCH /__admin/sessions/{id}` fields `ttlSeconds`, `addTokens`, `addMasks` | key-based `lifecycle`, `match_tokens`, `override_and_mask` |
| `keyPrefix` (first 8 characters) in session reads; the raw key only at creation | `02_shadow_mocks/key_based/lifecycle.feature` |
| `respond.matchedHeader` config switch for `X-Shadow-Mock-Matched` | `02_shadow_mocks/key_based/layered_resolution.feature` |
| `X-Shadow-Mock-Usage-Warning` response header on soft-cap overage | `04_saas/plans_and_limits/rate_and_session_limits.feature` |
| Error codes: `unknown_org`, `unknown_url_based_mock`, `ambiguous_session`, `token_in_use`, `ttl_exceeds_maximum`, `ttl_exceeds_plan_maximum`, `rate_limited`, `monthly_request_cap`, `egress_blocked`, `not_available_in_saas` | Throughout |

## Running

```bash
# Engine, both modes, phases up to P1
godog run --tags='~@wip && ~@perf && (@P0 || @P1)' features/

# SaaS only
godog run --tags='@saas && ~@management-app' features/

# Management app
npx cucumber-js features/05_management_app --tags '@management-app'
```

## Running the suite

```bash
make acceptance                                   # standalone engine, phases P0 to P3
ACCEPTANCE_TAGS='@FR-7.4' go test ./test/acceptance/   # a subset, by any godog tag filter
```

`make test` (and so CI) also runs it, because the coverage targets count what the specs exercise.

The harness in `test/acceptance` starts the engine in process for each scenario, with its own
root directory and test clock, and drives it over HTTP. It runs what the engine implements:

| Excluded | Why |
|---|---|
| `@saas`, `@multi-instance` | SaaS needs the Postgres, Redis and Clerk work in P5 |
| `@P4` | The central server (DESIGN.md §15) |
| `@P5` | The hosted service, not run here (DESIGN.md §15) |
| `@perf` | Runs in the benchmark pipeline |
| `@wip` | Not runnable yet. The comment above each tag says what is missing: a TLS listener in the harness, the audit log, detection metrics, a counting store wrapper, the blob store, key redaction, the Docker image, a WireMock Java client, and a separate process to measure memory |

Scenarios without a mode tag run once, as standalone. When a scenario's work is built, remove
its `@wip` tag (and its comment) in the same pull request.
