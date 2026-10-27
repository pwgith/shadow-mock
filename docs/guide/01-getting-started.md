# 1. Getting started

## Install

Download the binary for your system from the project's releases page (use the newest `v` release; `edge` is the latest build of `main`), or build it with Go 1.26
or later:

```bash
go build -o shadowmock ./cmd/shadowmock
```

Check it runs:

```bash
./shadowmock --version
```

The release binaries are not code-signed yet. On Windows you may see an "unknown publisher"
warning the first time you run one. Run `Unblock-File` on the file in PowerShell, or untick
"Always ask before opening this file" in the dialog.

## Run

Shadow Mock serves from a root directory. Start it in a folder of your choice:

```bash
./shadowmock --port 8080 --root-dir ./my-mocks
```

It listens on port 8080 and serves mock traffic and the admin API on the same port. The admin API
lives under `/__admin`.

Check it is alive:

```bash
curl localhost:8080/__admin/health
curl localhost:8080/__health
```

## The root directory

The root directory uses WireMock's layout, so an existing WireMock folder works unchanged:

```
my-mocks/
  mappings/      one JSON file per mapping, or several mappings per file
  __files/       response bodies referenced by bodyFileName
```

Mapping files are loaded when the server starts, and again whenever they change while it runs
(hot reload is on by default).

A mapping file holds one mapping:

```json
{
  "request":  { "method": "GET", "urlPath": "/hello" },
  "response": { "status": 200, "body": "Hello" }
}
```

or several:

```json
{ "mappings": [ { "request": { "urlPath": "/a" }, "response": { "status": 200 } },
                { "request": { "urlPath": "/b" }, "response": { "status": 404 } } ] }
```

Unknown fields in a mapping are kept, not dropped. Mappings that use WireMock scenarios
(`scenarioName` and its state fields) are reported and skipped. See
[Call-count responses](05-call-count-responses.md) for the replacement.

## Your first mapping through the API

```bash
curl -X POST localhost:8080/__admin/mappings -d '{
  "request":  { "method": "GET", "urlPath": "/hello" },
  "response": { "status": 200, "body": "Hello", "headers": { "Content-Type": "text/plain" } }
}'

curl -i localhost:8080/hello
```

Mappings added through the API live in memory. To write them to the `mappings/` folder, call
`POST /__admin/mappings/save`.

## When nothing matches

A request that matches no mapping gets a `404` whose body lists the closest mappings and why they
did not match. See [Verification and debugging](09-verification-and-debugging.md).

## Stopping

Press Ctrl+C, or call `POST /__admin/shutdown`.

## Next

* Learn to [match requests](02-matching-requests.md).
* Skip ahead to [shadow mocks](06-shadow-mocks.md) if sharing one server between many tests is
  why you are here.
