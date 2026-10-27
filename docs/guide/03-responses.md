# 3. Building responses

A mapping's `response` says what to send back.

```json
{
  "request": { "urlPath": "/orders/7" },
  "response": {
    "status": 200,
    "statusMessage": "OK",
    "headers": { "Content-Type": "application/json", "X-Source": "shadow-mock" },
    "jsonBody": { "id": 7, "state": "SHIPPED" }
  }
}
```

## Bodies

Use one of these:

| Field | Body |
|---|---|
| `body` | A text body |
| `jsonBody` | A JSON value. It is serialised for you, and `Content-Type` is not set automatically, so add the header |
| `base64Body` | Binary content, base64 encoded |
| `bodyFileName` | The name of a file in `__files/`, sent as the body |

```json
{ "response": { "status": 200, "bodyFileName": "orders/7.json" } }
```

Files can also be managed through the admin API (`/__admin/files`).

## Delays

| Field | Effect |
|---|---|
| `fixedDelayMilliseconds` | Wait this long before responding |
| `delayDistribution` | A random delay. `{ "type": "uniform", "lower": 100, "upper": 200 }` or `{ "type": "lognormal", "median": 80, "sigma": 0.1 }` (milliseconds) |
| `chunkedDribbleDelay` | Send the body in `numberOfChunks` pieces spread over `totalDuration` milliseconds, to simulate a slow connection |

```json
{ "response": { "status": 200, "body": "0123456789",
                "chunkedDribbleDelay": { "numberOfChunks": 5, "totalDuration": 1000 } } }
```

A delay counts as in flight until the response finishes. If the client gives up and disconnects,
the engine stops the delay and releases the request.

## Faults

`fault` breaks the connection instead of answering:

| Fault | What the client sees |
|---|---|
| `CONNECTION_RESET_BY_PEER` | The connection is reset |
| `EMPTY_RESPONSE` | The connection closes with no response |
| `MALFORMED_RESPONSE_CHUNK` | A valid start, then a broken chunk |
| `RANDOM_DATA_THEN_CLOSE` | Garbage, then the connection closes |

```json
{ "request": { "urlPath": "/flaky" }, "response": { "fault": "CONNECTION_RESET_BY_PEER" } }
```

Faults are real network behaviour. If Shadow Mock runs behind a proxy or load balancer, the client
may see a proxy error such as 502 instead of the raw fault.

## Gzip

Responses are gzip-compressed when the client sends `Accept-Encoding: gzip`. Turn this off for one
mapping with `"gzipDisabled": true`, or for everything with `respond.gzip: "off"` in the
[configuration](11-configuration.md).

## Proxying

A response can forward the request to a real server instead of answering itself:

```json
{
  "request": { "urlPathPattern": "/payments/.*" },
  "response": {
    "proxyBaseUrl": "https://payments.example.com",
    "additionalProxyRequestHeaders": { "X-Proxied-By": "shadow-mock" },
    "removeProxyRequestHeaders": ["X-Secret"],
    "proxyUrlPrefixToRemove": "/payments"
  }
}
```

* The request's path (less `proxyUrlPrefixToRemove`) and query are added to `proxyBaseUrl`.
* Hop-by-hop headers are dropped. `additionalProxyRequestHeaders` adds headers and
  `removeProxyRequestHeaders` removes them.
* The upstream's status, headers and body are returned to the client.
* If the upstream cannot be reached, the client gets a `502` with the error code `bad_gateway`.

Proxy mappings are useful for mocking one endpoint while the rest of an API stays real. Give the
proxy mapping a low priority so specific stubs win.

When Shadow Mock runs in a restricted environment, `egress.blockPrivate: true` stops proxying and
webhooks from reaching private, loopback and link-local addresses. The client then gets
`egress_blocked`.

## Unmatched requests

A request that no mapping matches gets a `404` with a diagnostic body naming the nearest mappings.
See [Verification and debugging](09-verification-and-debugging.md).

## Templated and call-count responses

Any response can use [templating](04-templating.md), and a mapping can give different responses
on different calls: see [Call-count responses](05-call-count-responses.md).
