# 14. Troubleshooting

## A request returns 404 and I expected a stub

The `404` body lists the closest mappings and why they did not match. Read it first. Then:

* Check the method, path and query. `url` matches the query string exactly, while `urlPath`
  ignores it.
* Check headers and bodies. Every field in a mapping's `request` must match.
* Use `POST /__admin/near-misses/request` with your request to see the distances.
* Use `POST /__admin/mappings/dry-run` to see which mapping would answer, from which layer.
* Remember layers. A request in a shadow mock only sees that session's mappings, then the global
  ones.

## My shadow mock does not seem to apply

* **Is the key reaching the server?** Send it in the `X-Shadow-Mock-Key` header, or check that it
  appears in the URL, a header value or the body.
* **Is the key correct?** The key is shown only when the session is created. If the response
  says `404` for the session or the request falls through to global mappings, the session may
  have **expired**. Check with `GET /__admin/sessions/{id}`, and extend it with `keepalive`.
* **Is the body scanned?** Body scanning applies only to the content types in
  `sessions.detect.contentTypes`, and only the first `maxScanBytes`.
* **Is another stage finding a different key first?** Detection stops at the first stage that finds
  a key. A key in a header wins over one in the body.
* **Was it ambiguous?** With `ambiguity: reject`, a request containing two sessions' keys fails
  with `409`. Set `respond.matchedHeader: true` and check `X-Shadow-Mock-Matched`.

## A URL-based shadow mock returns 404

The id is wrong, or the mock has expired. This never falls back to the global mappings. Create a
new one or extend the old one.

## Call counts are not what I expect

Counters are per mapping and per scope. A shadow mock has its own, and the global traffic shares
one. Replacing a mapping resets its counter. List them with `GET /__admin/counters` or
`GET /__admin/sessions/{id}/counters`, and reset with `POST .../counters/reset`.

## Scenario mappings are skipped or rejected

`scenarios_not_supported` means the mapping uses WireMock scenarios. See
[Coming from WireMock](13-coming-from-wiremock.md).

## Changes to mapping files do not show

Hot reload needs `storage.global.watch: true` (the default). Reload by hand with
`POST /__admin/mappings/reset`. A mapping with an error is reported in the log and skipped, so
look there.

## API-created mappings disappear after a restart

Mappings created through the API live in memory. Save them with `POST /__admin/mappings/save`.

## Template output is wrong or is `template_error`

* Templating must be on for the mapping (`"transformers": ["response-template"]`), or globally.
* Quote string arguments in single quotes: `{{jsonPath request.body '$.id'}}`.
* Look in the log for the failing helper and its arguments.

## Proxy or webhook calls fail

* `bad_gateway`: the upstream could not be reached. Check the URL and network.
* `egress_blocked`: `egress.blockPrivate` is on and the target is a private address.
* Failed webhooks are logged and do not change the response.

## The client sees a reset or 502 where I set a fault

Faults are real connection behaviour, and a proxy or load balancer in front of Shadow Mock may
turn them into its own error. Test faults on a direct connection.

## 413 body_too_large

The request body is over `limits.maxBodyBytes` (10 MiB by default).

## 403 ip_not_allowed

The client's address is not in `tenancy.local.ipAllowlist`. Behind a proxy, check
`server.clientIp`: the address Shadow Mock sees may be the proxy's.

## Windows says the publisher is unknown

The release binaries are not code-signed yet. See [Getting started](01-getting-started.md).

## Getting more information

Run with `--verbose` for debug logs, and use `log.format: json` if you collect logs. The
journal (`GET /__admin/requests`) shows what the server received and how it answered each
request.
