# 7. Record and playback

Recording captures real traffic to an upstream service and turns it into mappings, so tests can
play it back without the upstream.

## Record

```bash
curl -X POST localhost:8080/__admin/recordings/start -d '{
  "targetBaseUrl": "https://claims.example.com"
}'
```

While recording, requests that no mapping matches are forwarded to `targetBaseUrl`, and the
upstream's answers are captured. Send your traffic through Shadow Mock as usual:

```bash
curl localhost:8080/claims/1
```

Check the state:

```bash
curl localhost:8080/__admin/recordings/status     # "Recording" or "Stopped"
```

Stop, and receive the mappings:

```bash
curl -X POST localhost:8080/__admin/recordings/stop
```

The response lists the new mappings. They are saved (`persist` defaults to true), so they are
in your `mappings/` folder and serve straight away without the upstream.

### Recording options

```json
{
  "targetBaseUrl": "https://claims.example.com",
  "filters": { "urlPathPattern": "/claims/.*" },
  "captureHeaders": { "Accept": {} },
  "requestBodyPattern": { "matcher": "equalToJson", "ignoreArrayOrder": true },
  "persist": true,
  "repeatsAsCallResponses": true,
  "extractBodyCriteria": { "textSizeThreshold": "100 kb", "binarySizeThreshold": "0 kb" }
}
```

| Option | Effect |
|---|---|
| `targetBaseUrl` | Where unmatched requests are forwarded and recorded from |
| `filters` | Only record requests that match this request pattern |
| `captureHeaders` | Request headers to include in the recorded matching, with an optional matcher for each |
| `requestBodyPattern` | How to match recorded request bodies. Without it, a JSON body is matched with `equalToJson`, an XML body with `equalToXml`, and anything else with `equalTo` |
| `persist` | Save the mappings to the `mappings/` folder. Default `true` |
| `repeatsAsCallResponses` | Combine identical requests that got different answers into one mapping with [call-count responses](05-call-count-responses.md). This replaces WireMock's `repeatsAsScenarios`. Without it, a repeated request is one mapping that gives the last answer |
| `extractBodyCriteria` | Keep response bodies of at least this size in `__files` and point to them with `bodyFileName`. Sizes are a number and `kb`, `mb` or `gb`. `outputFormat` is described under snapshots |

Requests that match a mapping already in place are served from it and not recorded.

### What a recorded mapping looks like

The mappings are shaped the way WireMock's are, so files recorded by either can be used by both:

* The mapping is named after the path (`/claims/2` is `claims_2`).
* A request with no query string is matched by `url`. With one, it is `urlPath` plus `queryParameters`, each value matched with `equalTo` (every value of a repeated parameter, with `hasExactly`).
* Bodies are kept as text in `body`, JSON as well; a binary body is `base64Body`.
* A request body is matched by its content type unless you set `requestBodyPattern`: JSON with `equalToJson` ignoring array order and extra elements, XML with `equalToXml`, anything else with `equalTo`.
* The `Date` header of the upstream's answer is kept.
* Mappings are returned newest first.

## Snapshot

A snapshot builds mappings from requests Shadow Mock **already proxied**, without a recording
having been running:

```bash
curl -X POST localhost:8080/__admin/recordings/snapshot -d '{ "persist": true }'
```

Snapshots use the same options as a recording (`filters`, `captureHeaders`,
`requestBodyPattern`, `repeatsAsCallResponses`, `extractBodyCriteria`). With `"outputFormat": "IDS"` the reply is `{"ids": [...]}` instead of the mappings. They look at requests served by mappings with a
`proxyBaseUrl` ([proxying](03-responses.md)).

## Recording into a session

Recordings work for shadow mocks too, using the session's own admin path:

```bash
curl -X POST localhost:8080/__admin/sessions/$ID/recordings/start -d '{ "targetBaseUrl": "https://claims.example.com" }'
```

Only traffic belonging to that session is recorded, and the mappings go into that session. The most
specific active recording wins when several are running.

## Notes

* Recording forwards real requests, including their headers and bodies, to the upstream. Use a
  test environment.
* If the upstream cannot be reached, the client receives a `500` that says so, as WireMock sends.
* While a recording runs, requests that were forwarded show as unmatched in the journal.
