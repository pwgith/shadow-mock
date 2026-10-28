# 11. Configuration

Shadow Mock works with no configuration. To change the defaults, use a YAML file, environment
variables or command-line flags. When a setting is given more than once, **flags beat environment
variables, which beat the file, which beats the defaults**.

Unknown keys in the file are an error, so typos fail at startup instead of being ignored.

```bash
./shadowmock --config shadowmock.yaml
```

## Command-line flags

| Flag | Meaning |
|---|---|
| `--config` | Path of the YAML file |
| `--port` | Mock port. Shorthand for `--listen-mock :<port>` |
| `--listen-mock` | Mock listener address, such as `:8080` |
| `--listen-admin` | A separate listener address for the admin API |
| `--root-dir` | The directory holding `mappings/` and `__files/` |
| `--verbose` | Debug logging |
| `--version` | Print the version and exit |

## Environment variables

| Variable | Setting |
|---|---|
| `SHADOWMOCK_MODE` | `mode` |
| `SHADOWMOCK_LISTEN_MOCK` | `listen.mock` |
| `SHADOWMOCK_LISTEN_ADMIN` | `listen.admin` |
| `SHADOWMOCK_ROOT` | `root` |
| `SHADOWMOCK_LOG_LEVEL` | `log.level` |
| `SHADOWMOCK_LOG_FORMAT` | `log.format` |
| `SHADOWMOCK_PUBLIC_BASE_URL` | `server.publicBaseUrl` |
| `SHADOWMOCK_JOURNAL_MAX_ENTRIES` | `storage.journal.hot.maxEntries` |
| `SHADOWMOCK_LOG_REQUESTS` | `log.requests` |
| `SHADOWMOCK_TRACING_ENABLED` | `tracing.enabled` |
| `SHADOWMOCK_TRACING_ENDPOINT` | `tracing.endpoint` |

## The YAML file

Defaults are shown. A file only needs the settings you want to change.

```yaml
mode: standalone              # standalone | server. server needs auth.driver: token (below)
root: .                       # holds mappings/ and __files/
adminPrefix: /__admin         # where the admin API lives

listen:
  mock: ":8080"
  admin: ""                   # set to serve the admin API on its own port

admin:
  globalWrites: allow         # allow | deny
  allowedHosts: []            # Host names the admin API and metrics answer to; empty allows any
  allowedOrigins: []          # web pages that may make admin changes from a browser; empty allows none

auth:
  driver: noop                # noop | token. token puts the admin API behind bearer tokens
  tokens: []                  # name, role (viewer, editor, admin) and one of token, tokenEnv, tokenSha256

storage:
  global:
    driver: fs                # fs | memory
    watch: true               # hot reload of mapping files
  sessions:
    driver: memory            # memory | fs (keep sessions across restarts)
  journal:
    hot:
      maxEntries: 10000       # 0 turns the journal off
      maxBytes: 536870912     # memory budget for the whole journal (512 MiB); the oldest entries go first
      maxBodyBytes: 0         # cut request and response bodies to this size; 0 keeps them whole
  blobs:
    driver: fs                # fs | memory
    dir: ./blobs

respond:
  gzip: auto                  # auto | off
  matchedHeader: false        # add X-Shadow-Mock-Matched to responses

templating:
  global: false               # template every response

egress:
  blockPrivate: false         # refuse proxy and webhook calls to private addresses

limits:
  maxBodyBytes: 10485760      # largest request body accepted (10 MiB)

sessions:
  defaultTtl: 1h
  maxTtl: 24h
  keyPrefix: smk_
  detect:
    headers: [X-Shadow-Mock-Key]
    queryParams: [shadow_mock_key]
    cookies: [shadow_mock_key]
    scanMetadata: true        # scan header values, the query string and the path
    scanBody: true
    maxScanBytes: 1048576
    contentTypes: [text/*, application/json, application/*+json, application/xml, application/x-www-form-urlencoded]
    decompress: true          # inflate gzip and deflate bodies before scanning
    minTokenLength: 6
    ambiguity: first          # first | reject | global
  urlBased:
    pathPrefix: /d            # only /d is supported
    idLength: 16              # at least 16
    defaultTtl: 24h
    maxTtl: 720h              # 30 days

tenancy:
  local:
    ipAllowlist: []           # CIDRs allowed to send mock traffic; empty allows everyone
    allowlistAdmin: false     # apply the allowlist to the admin API as well

server:
  publicBaseUrl: ""           # used to build baseUrl in responses
  clientIp:
    header: ""                # a forwarding header such as X-Forwarded-For; empty uses the connection address
    trustedHops: 0
    trustedProxies: []

log:
  level: info                 # debug | info | warn | error
  format: text                # text | json
  requests: false             # one line per mock request, with the trace id

tracing:
  enabled: false              # export OpenTelemetry spans
  endpoint: ""                # OTLP/HTTP collector, such as http://localhost:4318
  serviceName: shadowmock
  sampleRatio: 1              # share of new traces kept, 0 to 1
```

Durations are written as `30s`, `15m`, `1h`, `720h`.

### Notes on some settings

* **`root`** is where `mappings/` and `__files/` live. Mapping sets for seeding sessions live in
  `<root>/mapping-sets`, and persisted sessions in `<root>/sessions`.
* **`admin.globalWrites: deny`** rejects changes to the global mappings, files and settings through
  the admin API, which is useful on a shared server where global mocks come from files. Session
  calls still work.
* **`storage.sessions.driver: fs`** saves sessions to disk so that they survive a restart. The
  default `memory` loses them.
* **`sessions.detect.contentTypes`** limits which bodies are scanned for session keys.
  `maxScanBytes` bounds how much of each is read.
* **`server.publicBaseUrl`** sets the address returned as `baseUrl` for URL-based shadow mocks.
  Set it when Shadow Mock is behind a proxy, so the URLs it hands out are ones clients can use.
* **`egress.blockPrivate`** is meant for shared and hosted servers. It stops proxying and webhooks
  being used to reach internal addresses.
* **`storage.journal.hot.maxBytes`** limits the memory the journal may hold. Without it, entries times body size is unbounded, because a request body can be up to `limits.maxBodyBytes`.
* **`limits.maxBodyBytes`** caps request bodies. Larger ones get `413 body_too_large`.

Some options (for example `storage.*.driver` values such as `postgres`, `redis` and `s3`, and
`auth.driver: jwt`) belong to the planned hosted service. They are rejected at startup
with a clear message until that exists.
