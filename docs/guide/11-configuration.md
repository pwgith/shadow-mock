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
| `--healthcheck` | Ask a running server on this configuration for `/__health`, then exit 0 (healthy) or 1. Used by the Docker health check |

### WireMock's flags

Every flag in WireMock 3.13's `--help` is accepted, with a value after a space or after `=`, so a command
line or a Docker `command:` copied from a WireMock setup starts. Each flag does one of three things.

**Sets a configuration key.** The key can be set in the file as well.

| Flag | Setting |
|---|---|
| `--global-response-templating`, `--local-response-templating` | `templating.global` |
| `--disable-response-templating` | `templating.disabled`. Even a mapping that asks for a template is sent as written |
| `--enable-stub-cors` | `server.stubCors` |
| `--disable-gzip` | `respond.gzip: off` |
| `--no-request-journal` | `storage.journal.hot.maxEntries: 0` |
| `--max-request-journal-entries` | `storage.journal.hot.maxEntries` |
| `--logged-response-body-size-limit` | `storage.journal.hot.maxBodyBytes` |
| `--bind-address` | `listen.bindAddress`, put in front of a listener address that names only a port |
| `--disable-request-logging`, `--print-all-network-traffic` | `log.requests` |
| `--permitted-system-keys` | `templating.permittedSystemKeys` |
| `--filename-template` | `storage.global.filenameTemplate` |
| `--admin-api-basic-auth user:password` | `auth.driver: basic` and `auth.basic`. Every admin call, and the metrics, then need HTTP Basic authentication; mock traffic does not |
| `--proxy-all <url>` | `proxy.all`. Adds a mapping of priority 10 that sends every request no other mapping matches to the URL |
| `--record-mappings` | `proxy.record`. With `--proxy-all`, records what is proxied: each new request is written to `mappings/` as soon as it has been proxied, and a repeat of one already recorded is not written again |
| `--match-headers a,b` | `proxy.matchHeaders` |
| `--preserve-host-header` | `proxy.preserveHostHeader` |
| `--proxy-via host:port` | `proxy.via` |
| `--proxy-timeout <ms>` | `proxy.timeout` |
| `--allow-proxy-targets`, `--deny-proxy-targets` | `proxy.allowTargets`, `proxy.denyTargets`. Addresses, ranges written `a-b`, CIDR blocks and names with `*`. The allowed list is looked at first. A refused target is a 500 |
| `--proxy-pass-through` | `proxy.passThrough`, the starting value of the `proxyPassThrough` setting |
| `--https-port` | `tls.port`. Adds an HTTPS listener that serves what the mock listener serves; see "HTTPS and HTTP/2" below |
| `--disable-http` | `tls.disableHttp`. The mock listener is not opened. Needs `--https-port` |
| `--https-keystore`, `--keystore-type`, `--keystore-password`, `--key-manager-password` | `tls.keystore`, `tls.keystoreType`, `tls.keystorePassword`, `tls.keyManagerPassword` |
| `--https-truststore`, `--truststore-type`, `--truststore-password`, `--https-require-client-cert` | `tls.truststore`, `tls.truststoreType`, `tls.truststorePassword`, `tls.requireClientCert` |
| `--disable-http2-plain`, `--disable-http2-tls` | `tls.disableHttp2Plain`, `tls.disableHttp2Tls` |
| `--admin-api-require-https` | `tls.adminRequireHttps` |
| `--enable-browser-proxying` | `proxy.browser.enabled`; see "Browser proxy" below |
| `--ca-keystore`, `--ca-keystore-password`, `--ca-keystore-type` | `proxy.browser.caKeystore`, `caKeystorePassword`, `caKeystoreType` |
| `--trust-all-proxy-targets`, `--trust-proxy-target` | `proxy.browser.trustAll`, `proxy.browser.trustTargets` |
| `--disable-banner`, `--preserve-user-agent-proxy-header` | Nothing to change: Shadow Mock prints no banner and always passes the client's `User-Agent` on |

**Accepted with a warning at start-up, and ignored,** because they tune WireMock's Java server:
`--container-threads`, `--jetty-*`, `--async-response-enabled`, `--async-response-threads`,
`--max-http-client-connections`, `--disable-connection-reuse`, `--disable-extensions-scanning`,
`--disable-optimize-xml-factories-loading`, `--disable-strict-http-headers`, `--extensions`,
`--supported-proxy-encodings`, `--use-chunked-encoding`, `--timeout`, `--webhook-threadpool-size`,
`--max-template-cache-entries` and `--load-resources-from-classpath`.

`--port`, `--root-dir`, `--verbose`, `--version` and `--help` are as in the table above.

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

tls:                          # HTTPS and HTTP/2 (see "HTTPS and HTTP/2" below)
  port: 0                     # the HTTPS port; 0 means no HTTPS listener
  certFile: ""                # certificate chain in PEM, with keyFile
  keyFile: ""                 # private key in PEM
  keystore: ""                # or a PKCS12 or JKS file holding the certificate and key
  keystoreType: ""            # JKS | PKCS12. Empty reads a file as JKS if it is one, else as PKCS12
  keystorePassword: ""        # empty means "password", as in WireMock
  keyManagerPassword: ""      # the password of the key inside a JKS keystore; empty means the keystore's
  truststore: ""              # certificates of the clients to trust
  truststoreType: ""
  truststorePassword: ""
  requireClientCert: false    # refuse a client without a certificate the truststore vouches for
  disableHttp: false          # serve HTTPS only
  disableHttp2Plain: false    # no HTTP/2 without TLS (h2c)
  disableHttp2Tls: false      # no HTTP/2 over TLS
  adminRequireHttps: false    # answer admin calls made over plain HTTP with 403

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
  global:
    filenameTemplate: ""     # names new mapping files, for example "{{{lower method}}}-{{{id}}}.json"

respond:
  gzip: auto                  # auto | off
  matchedHeader: false        # add X-Shadow-Mock-Matched to responses

listen:
  bindAddress: ""          # put in front of a listener address that names only a port

proxy:
  all: ""                    # send every request no mapping matches to this URL
  record: false               # with all: record what is proxied; each new request is written to mappings/ at once
  matchHeaders: []            # request headers a recording matches on
  preserveHostHeader: false   # send the client's Host header upstream
  via: ""                     # a proxy server to go through, host:port or a URL
  timeout: 0s                 # one proxied call; 0 means 30 seconds
  allowTargets: []            # addresses, a-b ranges, CIDR blocks and *.names that may be proxied to
  denyTargets: []             # the same, that may not (looked at after allowTargets)
  passThrough: true           # starting value of the proxyPassThrough setting
  browser:                    # use Shadow Mock as a proxy server (see "Browser proxy" below)
    enabled: false
    caKeystore: ""            # the certificate authority, PKCS12 or JKS; empty makes one, kept in caDir
    caKeystorePassword: ""    # password by default, as in WireMock
    caKeystoreType: ""
    caDir: ""                 # where the generated authority is kept; ~/.shadowmock when empty
    trustAll: false           # accept any certificate an origin presents
    trustTargets: []          # hosts whose certificate is accepted whatever it is

mappings:
  unknownFields: reject       # reject | keep. reject answers 422 for a field a mapping may not have, as WireMock does

templating:
  global: false               # template every response
  disabled: false             # send every response as written, even one that asks for a template
  permittedSystemKeys: []     # environment variables the systemValue helper may read

egress:
  blockPrivate: false         # refuse proxy and webhook calls to private addresses

limits:
  maxBodyBytes: 10485760      # largest request body accepted (10 MiB)
  # The rest are off at 0. See "Set quotas" in the shared server chapter.
  maxSessions: 0              # active shadow mocks, key-based and URL-based together
  maxMappings: 0              # global mappings
  maxMappingsPerSession: 0    # mappings in one shadow mock
  maxDelay: 0s                # longest response delay or dribble
  maxConnections: 0           # open connections per listener
  requestsPerSecond: 0        # mock requests per second from each client address
  burst: 0                    # requests allowed at once (0 rounds the rate up)

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
  stubCors: false             # answer CORS preflight requests and add Access-Control-* headers to mock traffic
  serveFiles: true            # serve __files at /__files/<path>, and at /<path> when no mapping matches
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

* **HTTPS and HTTP/2.** With `tls.port` the server also listens for HTTPS on that port, at the address
  `listen.bindAddress` gives, and answers there what the mock listener answers (the admin API too, unless
  `listen.admin` gives it a listener of its own, which stays plain). The certificate comes from
  `certFile` and `keyFile`, or from `keystore` (PKCS12 or JKS), or, when neither is set, is made when the
  server starts: a self-signed certificate for `localhost`, `127.0.0.1` and `::1`, so a client has to be
  told to accept it. Setting `truststore` asks every client for a certificate and checks the ones that
  present one against it; `requireClientCert` turns away the rest. Over TLS the server speaks HTTP/2 as
  well as HTTP/1.1 (the client chooses by ALPN), and on the plain port it accepts HTTP/2 from a client
  that starts with it (h2c, "prior knowledge"); `disableHttp2Tls` and `disableHttp2Plain` turn each
  off. `adminRequireHttps` answers an admin call that arrived over plain HTTP with 403 and WireMock's
  error body `{"errors":[{"code":40,"title":"HTTPS is required for accessing the admin API"}]}`.
  The journal records `HTTP/2.0` as the protocol of an HTTP/2 request, as WireMock does.

* **Browser proxy.** With `proxy.browser.enabled` (or `--enable-browser-proxying`) a browser or an HTTP
  client can use Shadow Mock as its proxy server, on the mock port. A request sent through it is matched
  against the mappings as if it had been sent to Shadow Mock, whatever host it names, and one no mapping
  matches goes on to the host it named. The journal marks it `browserProxyRequest: true`, and counts a
  request passed on as matched, as WireMock does. For HTTPS the client sends `CONNECT`, and Shadow Mock ends
  the TLS itself: it makes a certificate for the host, signs it with a certificate authority, and answers
  the requests inside the tunnel the same way. The client has to trust that authority. With no
  `caKeystore` one is made the first time and kept in `caDir` (`~/.shadowmock` by default) as
  `ca-keystore.p12`, with its certificate beside it as `ca.pem` to import into a browser or trust store;
  the start-up log gives its SHA-256 fingerprint. Origins are checked: an origin whose certificate the system
  does not trust is answered with a 500 whose body starts "SSL failure trying to make a proxied request", unless
  `trustAll` is set or the host is in `trustTargets`. These two do not combine with `proxy.via`.
  What goes on to an origin follows the same rules as other proxying: `proxy.allowTargets`,
  `proxy.denyTargets`, `egress.blockPrivate`, `proxy.timeout`. A recording that is running records
  what the browser proxy passes on. HTTP/1.1 only: `CONNECT` over HTTP/2 is not served.
  **Think before turning this on.** It is off by default because a proxy that ends TLS sees everything its
  clients send. Anyone who can reach the port can send traffic through it (`tenancy.local.ipAllowlist` and
  `limits.requestsPerSecond` apply), anyone who gets the authority's key can impersonate any site to a client
  that trusts it, so keep `ca-keystore.p12` private and trust the authority only in the clients you test with,
  and `egress.blockPrivate` stops it being used to reach private addresses.

* **`root`** is where `mappings/` and `__files/` live. Mapping sets for seeding sessions live in
  `<root>/mapping-sets`, and persisted sessions in `<root>/sessions`.
* **`admin.globalWrites: deny`** rejects changes to the global mappings, files and settings through
  the admin API, which is useful on a shared server where global mocks come from files. Session
  calls still work.
* **`mappings.unknownFields`** decides what happens to a mapping field Shadow Mock does not know,
  such as `urlPaht`. With `reject` (the default, and what WireMock does) the admin API answers 422
  with a pointer to the field. With `keep` the field is stored and returned unchanged, which suits
  mappings that carry fields for other tools. A matcher without an operator is refused either way.
  Mapping files read from `mappings/` are not checked.
* **`storage.sessions.driver: fs`** saves sessions to disk so that they survive a restart. The
  default `memory` loses them.
* **`sessions.detect.contentTypes`** limits which bodies are scanned for session keys.
  `maxScanBytes` bounds how much of each is read.
* **`server.stubCors`** does what WireMock's `--enable-stub-cors` does for mock traffic and `__files`: a
  preflight request (`OPTIONS` with `Origin` and `Access-Control-Request-Method`) is answered 200 with the
  allowed methods and headers, and any request with an `Origin` gets `Access-Control-Allow-Origin` and
  `Access-Control-Allow-Credentials: true`. A header a mapping sets itself replaces the one added. The
  admin API is not covered: it refuses requests from other web sites on purpose.
* **`server.serveFiles`** serves the files in `__files/` over HTTP, as WireMock does: at `/__files/<path>`,
  and at `/<path>` when no mapping matches. A folder is a 403, and a name that climbs out of `__files/`
  is not served. Turn it off on a server whose files should only be reached through mappings.
* **`server.publicBaseUrl`** sets the address returned as `baseUrl` for URL-based shadow mocks.
  Set it when Shadow Mock is behind a proxy, so the URLs it hands out are ones clients can use.
* **`egress.blockPrivate`** is meant for shared and hosted servers. It stops proxying and webhooks
  being used to reach internal addresses.
* **`storage.journal.hot.maxBytes`** limits the memory the journal may hold. Without it, entries times body size is unbounded, because a request body can be up to `limits.maxBodyBytes`.
* **`limits.maxBodyBytes`** caps request bodies. Larger ones get `413 body_too_large`.
* **`limits.maxSessions`, `maxMappings`, `maxMappingsPerSession`, `maxDelay`, `maxConnections`, `requestsPerSecond` and `burst`** are quotas for a shared server. They are all off at `0`, so a standalone server is unrestricted. A negative value is a startup error.

Some options (for example `storage.*.driver` values such as `postgres`, `redis` and `s3`, and
`auth.driver: jwt`) belong to the planned hosted service. They are rejected at startup
with a clear message until that exists.
