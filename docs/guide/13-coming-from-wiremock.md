# 13. Coming from WireMock

Shadow Mock aims to be a drop-in replacement for WireMock 3.x standalone. Every pull request replays
the corpus against a real WireMock and compares the replies and request journals. The full list of
differences is in [COMPATIBILITY.md](../COMPATIBILITY.md).

## What works unchanged

* The `mappings/` and `__files/` folder layout, and mapping JSON, including fields Shadow Mock
  does not use, which are kept.
* The admin API under `/__admin`, including mappings, files, requests, near misses, recordings,
  settings and health.
* Request matching: all the URL, header, query, cookie, form, body, JSON, JSON schema, XML and
  XPath and multipart matchers, with priorities.
* Responses: bodies, files, headers, delays, faults, gzip and proxying.
* Response templating with WireMock's Handlebars helpers.
* Record and playback, and webhooks.
* WireMock's client libraries for Java, .NET, JavaScript and Python, for the calls above.
* WireMock's command-line flags. A command line or Docker `command:` copied from a WireMock setup starts;
  [Configuration](11-configuration.md#wiremocks-flags) says what each flag does.

## What is different

| WireMock | Shadow Mock |
|---|---|
| Scenarios (`scenarioName`, `requiredScenarioState`, `newScenarioState`) | Not supported. Use [call-count responses](05-call-count-responses.md) |
| `repeatsAsScenarios` when recording | `repeatsAsCallResponses` |
| Java extensions | Go extensions: `pkg/extension` for custom matchers |
| One set of mappings for everyone | Global mappings, plus [shadow mocks](06-shadow-mocks.md) for isolation |
| Global browser-proxy mode, HTTPS and HTTP/2 flags | Not supported yet. The flags are refused with a clear message |

## Converting a scenario

A WireMock scenario is a small state machine: each call moves to the next state, and the mapping
for that state answers.

A typical "fail twice, then succeed" scenario is three mappings in WireMock. In Shadow Mock it is
one mapping:

```json
{
  "request": { "method": "POST", "urlPath": "/payments" },
  "response": { "status": 200, "jsonBody": { "result": "ok" } },
  "callResponses": [
    { "calls": "1-2", "response": { "status": 503 } }
  ]
}
```

Polling ("pending, pending, complete") and rate limits are the same shape. See
[Call-count responses](05-call-count-responses.md).

Scenarios that react to **different** requests changing shared state (for example, a `POST` that
creates something a later `GET` then sees) have no direct equivalent. Shadow Mock's matching
never depends on state, by design, so tests cannot interfere. For these, give each test its own
shadow mock and create the mappings you need in it as the test runs, using the admin API.

A mapping file that contains scenario fields is reported and skipped when loading, never loaded
with the fields ignored, so you will see which ones need converting.

## Moving a test suite across

1. Start Shadow Mock with `--root-dir` pointing at your existing WireMock folder.
2. Run your tests against it. Look at the startup log for skipped scenario mappings, and for
   any mapping that is rejected.
3. Convert the scenarios.
4. To stop tests interfering with each other on a shared server, move each test onto its own
   [shadow mock](06-shadow-mocks.md). With a WireMock client, this means sending the session key
   in the `X-Shadow-Mock-Key` header.

## Behaviours to be aware of

* Templates that call helpers with unusual argument shapes can behave differently. If a template
  fails, the response is `template_error` and the log says why.
* Faults depend on the network between the client and Shadow Mock. Behind a proxy, a client may
  see a proxy error, not the raw fault.
* The `/__admin/scenarios` endpoints return `404`.
