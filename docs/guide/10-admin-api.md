# 10. Admin API

The admin API is served under `/__admin`. It follows WireMock's API, with additions for shadow mocks.
The machine-readable description is [api/openapi.yaml](../../api/openapi.yaml).

The admin prefix can be changed with `adminPrefix`, and the admin API can be moved to its own
port with `listen.admin`. See [Running a shared server](12-shared-server.md).

## Scope

Most calls exist twice: once for the global session, and once for a shadow mock under
`/__admin/sessions/{sessionId}/...`. The session version has the same shape, so the examples below
for `/mappings` apply to `/sessions/{id}/mappings` too.

You can also scope a global call to a session by sending the session's key in the
`X-Shadow-Mock-Key` header.

## Mappings

| Call | Purpose |
|---|---|
| `GET /mappings` | List mappings |
| `POST /mappings` | Create a mapping |
| `DELETE /mappings` | Delete every mapping |
| `GET /mappings/{id}` | Get one |
| `PUT /mappings/{id}` | Replace one. This resets its call counters |
| `DELETE /mappings/{id}` | Delete one |
| `POST /mappings/import` | Import several mappings (`{ "mappings": [...] }`) |
| `POST /mappings/reset` | Restore the mappings loaded from storage |
| `POST /mappings/save` | Write API-created mappings to the `mappings/` folder |
| `GET /mappings/unmatched` | Mappings that no request has matched |
| `DELETE /mappings/unmatched` | Delete those mappings |
| `POST /mappings/find-by-metadata` | Find mappings by their `metadata` |
| `POST /mappings/remove-by-metadata` | Delete mappings by their `metadata` |
| `POST /mappings/validate` | Check a mapping without saving it |
| `POST /mappings/dry-run` | Resolve a sample request without sending it |

## Files

`GET /files`, and `GET`, `PUT` and `DELETE /files/{name}` manage the response files in `__files/`.

## Journal and verification

| Call | Purpose |
|---|---|
| `GET /requests` | List journal entries. Narrow with `unmatched=true`, `matchingStub=<id>`, `limit=<n>` (0 shows none) and `since=<time>` |
| `DELETE /requests` | Clear the journal |
| `GET /requests/{id}`, `DELETE /requests/{id}` | One entry |
| `POST /requests/count` | Count requests matching a pattern |
| `POST /requests/find` | Find requests matching a pattern |
| `POST /requests/remove` | Remove requests matching a pattern. Answers `{ "serveEvents": [...] }`, the entries removed |
| `POST /requests/remove-by-metadata` | Remove the requests served by a mapping whose `metadata` matches a pattern |
| `GET /requests/unmatched` | Requests that matched nothing |
| `GET /requests/unmatched/near-misses` | Closest mappings for those |
| `POST /near-misses/request` | Closest mappings for one request |
| `POST /near-misses/request-pattern` | Journal requests close to a pattern |

## Call counters

| Call | Purpose |
|---|---|
| `GET /counters` | List counters |
| `POST /counters/reset` | Reset every counter to zero |
| `PUT /counters/{mappingId}` | Set one counter |
| `DELETE /counters/{mappingId}` | Reset one counter to zero |

## Recording

`POST /recordings/start`, `POST /recordings/stop`, `GET /recordings/status` and
`POST /recordings/snapshot`. Sessions have `start`, `stop` and `status`. See
[Record and playback](07-record-and-playback.md).

## Sessions

| Call | Purpose |
|---|---|
| `GET /sessions` | List active sessions |
| `POST /sessions/key-based` | Create a key-based shadow mock |
| `POST /sessions/url-based` | Create a URL-based shadow mock |
| `GET /sessions/{id}` | Get a session |
| `PATCH /sessions/{id}` | Rename, change the TTL, add or remove tokens and masks |
| `DELETE /sessions/{id}` | Delete a session and everything it owns |
| `POST /sessions/{id}/keepalive` | Extend the TTL by its original duration |

Create bodies and patch fields are in [Shadow mocks](06-shadow-mocks.md).

## System

| Call | Purpose |
|---|---|
| `GET /settings`, `POST /settings`, `PUT /settings` | Global settings. `POST` and `PUT` do the same. `proxyPassThrough` starts true and keeps its value when a body leaves it out |
| `GET /` | The mappings list, as `GET /mappings` |
| `GET /docs`, `GET /docs/swagger`, `GET /swagger-ui/` | An index of the documents, this API as an OpenAPI document, and Swagger UI (which loads its script from a CDN) |
| `POST /reset` | Reset mappings, the journal and call counters |
| `POST /shutdown` | Stop the server |
| `GET /health` | WireMock-style health |
| `GET /version` | The engine's version |
| `GET /stats/live` | Live request statistics: rate by layer, match rate, top unmatched paths |

Outside the admin prefix, `GET /__health` (liveness) and `GET /__ready` (readiness) are always
available.

## Console and command line

Both are clients of the admin API. They hold no behaviour of their own, so what they show is what the
API answers.

**The console** is a small web page served by the binary. It is off unless `console.enabled: true`, and
it is for standalone and central-server use: a managed-mode server refuses to start with it on.

```bash
./shadowmock --config shadowmock.yaml     # with console: { enabled: true }
# then open http://localhost:8080/__console/
```

It shows the journal (with an "unmatched only" filter, and for an unmatched request the closest mappings and
what each missed), the shadow mocks, the call counters, recordings, and a dry run. A shadow mock can be edited
in place: renamed, given a new lifetime, kept alive, and have its match tokens and masks added or removed.
Counters are listed for the global session or a shadow mock, and each can be set or reset. A recording can be
started and stopped for the global session or a shadow mock, and the mappings it captured are listed when it
stops. On a central server with admin tokens, the
page itself is served to anyone, but every call it makes needs the token, which you type into its header
field. The token is kept for the browser tab only.

**The command line** has sub-commands that each make one admin call and print the answer:

| Command | Calls |
|---|---|
| `shadowmock session create [--kind key-based\|url-based] [--name N] [--ttl 10m] [--token T]... [--no-global]` | `POST /sessions/{kind}`. `--kind` defaults to `key-based`; `--no-global` sets `inheritGlobal: false` (FR-7.13) |
| `shadowmock session list [--kind K] [--state active\|expired] [--name N] [--created-by W]` | `GET /sessions` |
| `shadowmock session delete ID` | `DELETE /sessions/{id}` |
| `shadowmock journal tail [--unmatched] [--limit N]` | `GET /requests`, newest first |
| `shadowmock recording start --target URL` | `POST /recordings/start` |
| `shadowmock recording stop`, `shadowmock recording status` | `POST /recordings/stop`, `GET /recordings/status` |

Every sub-command takes `--url` (the server's base URL, default `http://localhost:8080`, or `SHADOWMOCK_URL`;
on a shared host include the org, such as `https://mocks.example.com/acme`) and `--admin-prefix` (default
`/__admin`). A key or token is read from the environment, never from a flag, so that it does not show in a
process list: `SHADOWMOCK_KEY` (a Tool app key) or else `SHADOWMOCK_TOKEN`, sent as a bearer token.

The answer is printed as the API sent it, whatever its status. An answer of 400 or above also makes the
command exit 1.

## Journal answers

Every journal answer says whether the journal is switched off (`requestJournalDisabled`), and
`meta.total` in `GET /requests` counts what `unmatched` and `matchingStub` leave, before `since` and
`limit` cut the list. Each entry has `subEvents`: empty for a request a mapping served, and for a request
nothing matched a `REQUEST_NOT_MATCHED` event whose `data.report` holds the report that was the 404 body.
Deleting an id that is not in the journal is still a 200; an id that is not a UUID is a 400.

## Mapping files

A mapping that is `persistent`, or saved with `POST /mappings/save`, is written to `mappings/<name>-<id>.json`.
The name is the mapping's `name`, or `<method>-<url>` when it has none, in lower case with spaces as hyphens and
every character but letters, digits, dots, underscores and hyphens removed (`Get /users?id=1` becomes
`get-usersid1`). A mapping that is replaced keeps its file name. A stored mapping always has `request.method`;
it is `ANY` when the mapping gave none.

## Scenarios

`/__admin/scenarios` returns `404 not_supported`. See
[Call-count responses](05-call-count-responses.md).

## Errors

Errors are JSON with a stable machine-readable code:

```json
{ "error": "token_in_use", "message": "…" }
```

Mapping validation failures use WireMock's `{ "errors": [ ... ] }` shape, so WireMock clients can
read them. A mapping with a field Shadow Mock does not know, such as `urlPaht`, is a 422 whose
`source.pointer` names it (`/request/urlPaht`). Set `mappings.unknownFields: keep` to store such fields
instead (see [Configuration](11-configuration.md)).

Codes you may meet:

| Status | Codes |
|---|---|
| 404 | `not_found`, `not_supported`, `unknown_url_based_mock` |
| 403 | `forbidden`, `ip_not_allowed` |
| 409 | `token_in_use`, `recording_in_progress`, `ambiguous_session` |
| 413 | `body_too_large` |
| 422 | `validation_failed`, `scenarios_not_supported`, `ttl_must_be_positive`, `ttl_exceeds_maximum`, `token_too_short`, `parent_not_found` |
| 502 | `egress_blocked` on mock traffic |
| 500 | `template_error` on mock traffic |

Some codes in the OpenAPI file belong to the planned hosted service (authentication,
plan limits and organisations) and never occur when you run Shadow Mock yourself.

## Using WireMock clients

WireMock's Java, .NET, JavaScript and Python clients work against Shadow Mock for the calls above.
Point the client at the server's address. To use a shadow mock with a WireMock client, send the
session key with its admin calls in the `X-Shadow-Mock-Key` header.
