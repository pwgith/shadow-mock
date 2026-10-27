# 5. Call-count responses

A mapping can answer differently depending on how many times it has been called. Use it for
retries, polling, flaky dependencies and rate limits.

This replaces WireMock **scenarios**. Matching never depends on state: the call count only picks
which response a mapping gives, so tests cannot affect each other.

## Writing one

`response` is the default. Each entry in `callResponses` overrides it for the calls it names:

```json
{
  "request": { "method": "POST", "urlPath": "/payments" },
  "response": { "status": 200 },
  "callResponses": [
    { "calls": "1-2", "response": { "status": 503 } }
  ]
}
```

The first two calls get 503. From the third call on, the default 200 is used.

### Selectors

| Selector | Matches |
|---|---|
| `"calls": "3"` | The 3rd call only |
| `"calls": "2-4"` | Calls 2 to 4 |
| `"calls": "6+"` | Call 6 and every call after it |
| `"every": 3` | Every 3rd call (3, 6, 9, ...) |

The **first rule that matches** the current call wins, so put specific rules before general ones.
Call numbers start at 1. Each rule's `response` can use everything a normal response can:
templates, delays, faults, files and proxying.

### Polling until complete

```json
{
  "request": { "urlPath": "/jobs/7" },
  "response": { "status": 200, "jsonBody": { "state": "COMPLETE" } },
  "callResponses": [
    { "calls": "1-3", "response": { "status": 200, "jsonBody": { "state": "PENDING" } } }
  ]
}
```

Calls 1 to 3 return `PENDING`, and call 4 onwards returns `COMPLETE`.

### Rate limiting

```json
{
  "request": { "urlPath": "/api" },
  "response": { "status": 200 },
  "callResponses": [ { "calls": "100+", "response": { "status": 429, "headers": { "Retry-After": "1" } } } ]
}
```

### Using the number in a template

`{{request.callNumber}}` gives the call number in a templated response, so one response can vary
without a rule per call:

```json
{ "response": { "status": 200, "transformers": ["response-template"], "body": "call {{request.callNumber}}" } }
```

## What counts as a call

A call is a request that the mapping matched and served. Counters are kept **per mapping and per
scope**:

* Every shadow mock has its own counters, **including for global mappings that serve its
  requests**. A session's counters start at zero when it is created and are discarded with it.
* Plain global traffic shares one counter per mapping.

So two tests running at the same time, each with its own session key, each see their own 1st, 2nd
and 3rd call from the same global mapping. See [Shadow mocks](06-shadow-mocks.md).

Replacing a mapping (the same `id`) resets its counters.

## Managing counters

```bash
curl localhost:8080/__admin/counters
curl -X POST localhost:8080/__admin/counters/reset
```

`PUT /__admin/counters/{mappingId}` sets one counter to a value, and
`DELETE /__admin/counters/{mappingId}` resets one to zero. Session-scoped versions are under
`/__admin/sessions/{sessionId}/...`. See the [admin API](10-admin-api.md).

## Scenarios are rejected

A mapping with `scenarioName`, `requiredScenarioState` or `newScenarioState` is rejected with
`422 scenarios_not_supported` and a message pointing here. Files containing them are reported and
skipped on load. `/__admin/scenarios` returns `404 not_supported`. See [Coming from
WireMock](13-coming-from-wiremock.md) for how to convert a scenario.
