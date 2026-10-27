# Shadow Mock

Shadow Mock is an HTTP mocking server written in Go. It reproduces WireMock's functionality and
reads WireMock's mapping files, so existing stubs and clients work unchanged. It adds shadow
mocks, so many tests can share one mock deployment without interfering with each other. There
are three ways a request can be served:

| Mode | Use it when | How a request reaches it |
|---|---|---|
| **Global** | You want WireMock behaviour | Every request, by default |
| **Key-based shadow mock** | Many tests share one environment | A unique session key found anywhere in the request (header, query, cookie, path or body) |
| **URL-based shadow mock** | One API under test in CI | A unique base URL, `/d/<id>/...` |

A request is resolved against its own shadow mock first, then falls back to the global mocks.
Sessions expire on their own, so abandoned tests clean up after themselves.

Shadow Mock also replaces WireMock scenarios with **call-count responses**: a stub answers
differently on its 1st, 2nd or 3rd call, and each shadow mock counts from zero. Matching never
depends on state, so sessions never affect each other.

## What it supports

* WireMock 3.x mappings: URL, method, header, query, cookie, body, JSON, XML, XPath, JSON schema
  and multipart matching, plus custom matchers written in Go.
* Response templating with WireMock's Handlebars helpers.
* Fixed, random and log-normal delays, chunked dribble, gzip, and connection faults.
* Proxying, record and playback, webhooks and near-miss reports.
* The WireMock admin API under `/__admin`, plus session endpoints for shadow mocks.
* Hot reload of mapping files, health endpoints, and an OpenAPI description of the admin API
  in [api/openapi.yaml](api/openapi.yaml).

Scenarios (`scenarioName` and its state fields) are not supported and are rejected with a clear
error. Use `callResponses` instead.

## Install

Download a binary from the releases page, or build from source with Go 1.26 or later:

```bash
go build -o shadowmock ./cmd/shadowmock
```

## Usage

Start the server in a directory holding `mappings/` and `__files/`, as WireMock does:

```bash
./shadowmock --port 8080 --root-dir ./my-mocks
```

Add a mapping through the admin API:

```bash
curl -X POST localhost:8080/__admin/mappings -d '{
  "request":  { "method": "GET", "urlPath": "/hello" },
  "response": { "status": 200, "body": "Hello" }
}'
curl localhost:8080/hello
```

### Call-count responses

The `response` is the default. Each `callResponses` entry overrides it for the calls it names
(`"3"`, `"2-4"`, `"6+"`):

```json
{
  "request": { "urlPath": "/payments" },
  "response": { "status": 200 },
  "callResponses": [ { "calls": "1-2", "response": { "status": 503 } } ]
}
```

The first two calls get 503, and every call after that gets 200.

### Key-based shadow mocks

Create one for your test. The key is returned once:

```bash
curl -X POST localhost:8080/__admin/sessions/key-based \
  -d '{ "name": "checkout-declined-card", "ttlSeconds": 600 }'
```

Add mappings to it with `POST /__admin/sessions/{sessionId}/mappings`, then send the key with
your test traffic, in the `X-Shadow-Mock-Key` header by default:

```bash
curl -H "X-Shadow-Mock-Key: smk_..." localhost:8080/payments
```

Only requests carrying the key see that session's mappings. Everyone else keeps getting the
global mocks. You can also register match tokens, such as a test customer id, that select the
session when they appear anywhere in a request.

### URL-based shadow mocks

```bash
curl -X POST localhost:8080/__admin/sessions/url-based -d '{ "name": "orders-api-ci" }'
```

The response gives a base URL such as `http://localhost:8080/d/<id>`. Point the API under test at
it for its outbound calls.

### Configuration

Configuration comes from a YAML file (`--config`), `SHADOWMOCK_*` environment variables and
flags, with flags winning. Common flags:

| Flag | Meaning |
|---|---|
| `--port`, `--listen-mock` | Mock listener, `:8080` by default |
| `--listen-admin` | Serve the admin API on its own listener |
| `--root-dir` | Directory holding `mappings/` and `__files/` |
| `--config` | YAML configuration file |
| `--verbose` | Debug logging |

For example, to keep the admin API on a private port and restrict mock traffic to one network:

```yaml
listen: { mock: ":8080", admin: ":9090" }
tenancy: { local: { ipAllowlist: ["10.20.0.0/16"] } }
```

## Documentation

The [user guide](docs/guide/README.md) covers everything from first run to running a shared server.

* [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md): what it does and why.
* [docs/DESIGN.md](docs/DESIGN.md): how it works.
* [features/](features/): the acceptance specs, written in Gherkin and run in CI.
* [CONTRIBUTING.md](CONTRIBUTING.md): how to contribute, including the sign-off and licence grant.
* [docs/CODING_STANDARDS.md](docs/CODING_STANDARDS.md): how to contribute code.

Run the checks with `make lint` and `make test`, and the acceptance specs with `make acceptance`.

## Status

The engine, shadow mocks and WireMock parity are built. The central server (metrics, tracing,
hardening) is in progress, and a hosted multi-tenant service is planned.

## Licence

Shadow Mock is licensed under the [GNU Affero General Public License v3.0](LICENSE). You can use,
modify and run it freely, including inside your company and as a test tool. If you modify it and
offer it to others over a network, you must make your modified source available under the same
licence.

Contributions are accepted under a Developer Certificate of Origin sign-off and a licence grant to the project owner, so the project can also be offered under commercial terms. See [CONTRIBUTING.md](CONTRIBUTING.md). Commercial licences for other terms are available from the project owner.
