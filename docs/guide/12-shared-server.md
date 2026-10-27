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

## Logs

```yaml
log:
  level: info
  format: json
```

JSON logs suit log collectors. Session keys are shown in shortened form.

## A starting configuration

```yaml
listen:
  mock: ":8080"
  admin: ":9090"
admin:
  globalWrites: deny
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
