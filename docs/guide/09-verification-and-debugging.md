# 9. Verification and debugging

## The request journal

Shadow Mock records every request it serves: the request, the mapping that answered it (if any),
the response, and how long it took. The journal holds the most recent 10,000 entries by default
(`storage.journal.hot.maxEntries`; `0` turns it off). Bodies are kept whole, as in WireMock, so a test can read a large response back from the journal. The whole journal is also held to a memory budget (`storage.journal.hot.maxBytes`, 512 MiB by default): when the entries together outgrow it, the oldest are dropped first, and the newest entry is always kept. To cut long bodies instead, set `storage.journal.hot.maxBodyBytes`.

```bash
curl localhost:8080/__admin/requests                    # most recent first
curl localhost:8080/__admin/requests/<id>               # one entry
curl -X DELETE localhost:8080/__admin/requests          # clear it
```

Each entry also records which session and layer served the request, and, for requests that went
through detection, how the session was found.

The journal reflects every request already served by the time the client has its response, so a
test can verify straight after it calls.

## Verifying that something was called

Count the requests that match a pattern:

```bash
curl -X POST localhost:8080/__admin/requests/count -d '{
  "method": "POST", "urlPath": "/payments",
  "bodyPatterns": [ { "matchesJsonPath": "$.amount" } ]
}'
```

The pattern uses the same syntax as a mapping's `request`. The response gives `{"count": N}`.

Find them instead of counting with `POST /__admin/requests/find`, and remove them from the journal
with `POST /__admin/requests/remove`.

**For tests on a shared server, use the session version**, such as
`/__admin/sessions/{id}/requests/count`. A session's journal holds only its own requests, so other
tests' traffic cannot affect your count.

## Unmatched requests

```bash
curl localhost:8080/__admin/requests/unmatched
curl localhost:8080/__admin/mappings/unmatched    # mappings that have never matched
```

When a request matches no mapping, the `404` body lists the closest mappings and why each did not
match. The same report is available for any request:

## Near misses

```bash
curl -X POST localhost:8080/__admin/near-misses/request -d '{ "method": "GET", "url": "/oders/7" }'
curl localhost:8080/__admin/requests/unmatched/near-misses
curl -X POST localhost:8080/__admin/near-misses/request-pattern -d '{ "urlPath": "/orders/7" }'
```

The first returns the mappings closest to a request. The second does so for every unmatched
request in the journal. The third finds journal requests that nearly match a pattern, which helps
when a verification count is zero and you do not know why.

Results are ranked by `matchResult.distance`: lower is closer.

## Trying a request without sending it

`POST /__admin/mappings/dry-run` resolves a sample request through the stack and tells you which
mapping would answer, from which layer, how the session was detected and what the response would
be, without recording anything:

```bash
curl -X POST localhost:8080/__admin/mappings/dry-run -d '{
  "request": { "method": "GET", "url": "/payments" },
  "sessionId": "<session id>"
}'
```

`POST /__admin/mappings/validate` checks a mapping without saving it.

## Logs

Logs go to standard error. Use `--verbose` (or `log.level: debug`) for detail, and
`log.format: json` for structured logs. Session keys are never logged in full.

## Which layer answered?

Set `respond.matchedHeader: true` to add an `X-Shadow-Mock-Matched` header to responses. See
[Shadow mocks](06-shadow-mocks.md).
