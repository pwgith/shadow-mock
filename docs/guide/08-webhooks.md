# 8. Webhooks

A mapping can make an outbound HTTP call after it has served a request. Use this to simulate an
API that calls back, such as a payment provider notifying you when a payment settles.

```json
{
  "request":  { "method": "POST", "urlPath": "/payments" },
  "response": { "status": 202 },
  "serveEventListeners": [
    {
      "name": "webhook",
      "parameters": {
        "method": "POST",
        "url": "https://my-app.example.com/callback",
        "headers": { "Content-Type": "application/json" },
        "body": "{ \"paymentRef\": \"{{jsonPath originalRequest.body '$.ref'}}\", \"status\": \"SETTLED\" }"
      }
    }
  ]
}
```

`postServeActions` is accepted as the older name for the same thing.

## Parameters

| Parameter | Meaning |
|---|---|
| `method` | The HTTP method |
| `url` | Where to send it |
| `headers` | Request headers |
| `body` | The body. Use `base64Body` for binary content |
| `delay` | Wait before sending, for example `{ "type": "fixed", "milliseconds": 1000 }` |

The URL, headers and body are templated. They can read the request that triggered the webhook as
`originalRequest` (for example `originalRequest.body`, `originalRequest.headers.Name`), and the
response that was served.

## Behaviour

* The webhook is sent **after** the response is on its way, and never changes it. A webhook that
  fails, or is rejected by the target, is logged and does not affect the client.
* Webhooks are sent in the background. A small limit on how many can be in flight at once
  protects the server, so a burst can be refused. Refusals are logged.
* A mapping can list several webhooks.
* Webhook targets are subject to the same `egress.blockPrivate` rule as proxying. When it is on,
  webhooks cannot reach private, loopback or link-local addresses.
