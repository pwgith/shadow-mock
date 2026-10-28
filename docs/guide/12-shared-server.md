# 12. Running a shared server

Shadow Mock can run as a long-lived server shared by many teams. This page covers the settings
that matter when the server is not just on your own machine.

## Keep the admin API on a separate port

By default the admin API shares the mock port. On a shared network, give it its own listener so
you can firewall it separately:

```yaml
listen:
  mock: ":8080"
  admin: ":9090"
```

Then:

* Mock traffic uses port 8080, and `/__admin/...` on that port returns `404`.
* The admin API is on port 9090.

The same with flags: `--listen-mock :8080 --listen-admin :9090`.

## Put the admin API behind tokens

By default the admin API has no login: whoever can reach it can read every request in the journal,
change any mock and stop the server. On a shared server, turn on tokens:

```yaml
mode: server
auth:
  driver: token
  tokens:
    - { name: ops,  role: admin,  tokenEnv: SHADOWMOCK_OPS_TOKEN }
    - { name: ci,   role: editor, tokenEnv: SHADOWMOCK_CI_TOKEN }
    - { name: dash, role: viewer, tokenSha256: "<the 64 hex characters of the token's SHA-256>" }
```

Clients send the token as `Authorization: Bearer <token>`:

```bash
curl -H "Authorization: Bearer $SHADOWMOCK_CI_TOKEN" https://mocks.example.com:9090/__admin/sessions/key-based -d {}
```

| Role | May |
|---|---|
| `viewer` | Read mappings, the journal, sessions and counters, and the metrics |
| `editor` | Also create and change sessions, and their mappings, files and recordings |
| `admin` | Also change the global mappings, files and settings, reset, and stop the server |

Give a token in one of three forms, never more than one:

| Form | Use it when |
|---|---|
| `tokenEnv: NAME` | The secret comes from an environment variable or a secret store (best for containers) |
| `tokenSha256: "…"` | You want only a hash in the file. Make it with `printf %s "$TOKEN" | sha256sum` |
| `token: "…"` | The secret is in the file. The server warns about this at startup |

Make a token from a random source, at least 16 characters, with `openssl rand -base64 32`.
At least one token must have the `admin` role. Only the hash of each token is kept in memory, and
a call is compared with every token in constant time.

What needs a token and what does not:

| | Token needed |
|---|---|
| `/__admin/…` | Yes |
| `/__metrics` | Yes, a viewer token. Give Prometheus `authorization: { credentials: <token> }` |
| Mock traffic, including `/d/<id>/…` and session keys | No |
| `/__health`, `/__ready`, `/__admin/health` | No |

`mode: server` refuses to start without `auth.driver: token`, so a server in that mode is never
open by accident. A configuration that lists tokens but leaves `auth.driver` at `noop` also refuses
to start, because it would look protected and not be.

### Guarding against web pages

A web page you visit can send requests to a server that is only reachable from your network.
Three settings narrow that:

- **`admin.allowedHosts`**: the names clients use to reach the server, such as
  `["mocks.example.com"]`. A request with any other `Host` is refused, which defeats DNS rebinding.
  Without a list the server logs a warning at startup.
- **`admin.allowedOrigins`**: web pages that may make admin changes from a browser, for example a
  dashboard at `https://dashboard.example.com`. A page not on the list is refused.
- **`tenancy.local.allowlistAdmin: true`**: applies `ipAllowlist` to the admin API too.

## Restrict who can send mock traffic

Allow only your own networks:

```yaml
tenancy:
  local:
    ipAllowlist: ["10.20.0.0/16", "192.168.5.0/24"]
```

Requests from other addresses get `403 ip_not_allowed` before the body is read.

Behind a reverse proxy or load balancer, the connection address is the proxy's. Tell Shadow Mock
how to find the real client address, and which proxies to believe:

```yaml
server:
  clientIp:
    header: X-Forwarded-For
    trustedProxies: ["10.0.0.0/8"]
```

Shadow Mock uses the forwarding header only when the connection comes from a trusted proxy, so
clients cannot make up their own address.

## Protect global mocks

If the global mocks come from files that a team maintains, stop the admin API from changing them:

```yaml
admin:
  globalWrites: deny
  allowedHosts: [mocks.example.com]
```

Session calls still work, so tests can create their own shadow mocks.

## Keep sessions across restarts

```yaml
storage:
  sessions:
    driver: fs
```

Sessions are saved under `<root>/sessions` and restored when the server starts.

## Limit what mappings can reach

Proxy and webhook mappings make outbound requests from the server. On a shared server, block
private destinations so that mappings cannot be used to reach internal systems:

```yaml
egress:
  blockPrivate: true
```

Also keep the request body limit (`limits.maxBodyBytes`) at a size you are comfortable with.

## Health and metrics

| Endpoint | Purpose |
|---|---|
| `GET /__health` | Liveness: is the process up |
| `GET /__ready` | Readiness: is it ready for traffic |
| `GET /__admin/health` | WireMock-style health |
| `GET /__metrics` | Prometheus metrics |

These sit outside the admin prefix so a load balancer can use them. When the admin API has its own
listener, `/__metrics` is served there and is not exposed on the mock port.

### Metrics

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `shadowmock_requests_total` | counter | `org`, `method`, `status` | Mock requests served |
| `shadowmock_matches_total` | counter | `org`, `layer` | Requests answered by a mapping, by the layer it came from (`global`, `key-based`, `url-based`) |
| `shadowmock_detection_hits_total` | counter | `org`, `stage` | Requests assigned to a shadow mock, by the detection stage that found it (`Header`, `Query`, `Cookie`, `ScanMeta`, `ScanBody`) |
| `shadowmock_active_sessions` | gauge | none | Active key-based and URL-based shadow mocks |
| `shadowmock_automaton_patterns` | gauge | none | Keys and tokens in the session detector |
| `shadowmock_automaton_rebuild_seconds` | histogram | none | How long a detector rebuild takes |

Labels are limited to `org`, `layer`, `stage`, `status` and `method`. Session ids, session keys,
paths and client addresses are never labels, so the metrics stay small and do not leak who is
calling. Unmatched requests count in `shadowmock_requests_total` but not in
`shadowmock_matches_total`. Admin calls are not counted as mock requests.

A scrape configuration:

```yaml
scrape_configs:
  - job_name: shadowmock
    metrics_path: /__metrics
    static_configs:
      - targets: ["mocks.example.com:9090"]   # the admin listener, if you set one
```

Tracing and a Docker image are being added.

## Logs and traces

```yaml
log:
  level: info
  format: json
  requests: true      # one line for each mock request
tracing:
  enabled: true
  endpoint: http://otel-collector:4318
```

JSON logs suit a log collector. Session keys are shown in shortened form.

With `log.requests: true` the server writes one line for each mock request:

```json
{"time":"…","level":"INFO","msg":"request served","method":"GET","path":"/payments","status":200,
 "layer":"key-based","stage":"Header","duration_ms":3,"session_id":"ses_…",
 "trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"b7ad6b7169203331"}
```

The line never holds the query string, a body or a header, because a query can carry a session key
or a token.

With `tracing.enabled: true` the server exports OpenTelemetry spans to an OTLP/HTTP collector, one
for each mock request (`mock GET`) and one for each admin call (`admin POST`). It continues the
trace in a caller's W3C `traceparent` header, so a test run's trace includes the mock server. A
call proxied to an upstream carries the trace on. A mock span has these attributes:

| Attribute | Meaning |
|---|---|
| `http.request.method`, `url.path`, `http.response.status_code` | The request and its answer. The path has no query |
| `shadowmock.layer` | `global`, `key-based` or `url-based`; empty when nothing matched |
| `shadowmock.matched` | Whether a mapping answered |
| `shadowmock.stub_id` | The mapping that answered |
| `shadowmock.session_id` | The session, when there was one |
| `shadowmock.detection_stage` | The stage that found the session |

`sampleRatio` is the share of new traces kept. A trace a caller already sampled is kept, and one
it chose not to sample is not recorded. Log lines carry the caller's `trace_id` even when tracing is
off.

## A starting configuration

```yaml
mode: server
auth:
  driver: token
  tokens:
    - { name: ops, role: admin, tokenEnv: SHADOWMOCK_OPS_TOKEN }
    - { name: ci, role: editor, tokenEnv: SHADOWMOCK_CI_TOKEN }
listen:
  mock: ":8080"
  admin: ":9090"
admin:
  globalWrites: deny
  allowedHosts: [mocks.example.com]
storage:
  sessions:
    driver: fs
tenancy:
  local:
    ipAllowlist: ["10.0.0.0/8"]
server:
  publicBaseUrl: https://mocks.example.com
  clientIp:
    header: X-Forwarded-For
    trustedProxies: ["10.0.0.0/8"]
egress:
  blockPrivate: true
log:
  format: json
```
