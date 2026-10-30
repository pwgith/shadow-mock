# Verified parity with WireMock

On 2026-10-28 Shadow Mock was compared with WireMock's documentation, its test suite and a real WireMock
3.13.2, and the differences were listed as gaps. [PARITY_PLAN.md](PARITY_PLAN.md) closed all of them in ten
workstreams. This file is what remains of the list: a statement of what is checked against a real WireMock,
how to run it, and what is not.

The original list, with its tables, is in git: `git show 03bb43b:docs/WIREMOCK_GAPS.md`. The differences that
were kept on purpose are in [COMPATIBILITY.md](COMPATIBILITY.md). Anything else that differs is a bug.

## What is compared with a real WireMock

Each test below sends the same requests to Shadow Mock and to WireMock 3.13.2 and compares the answers and,
where it matters, the request journal. They run on every pull request in the `wiremock-reference` CI job,
against the servers `scripts/wiremock-reference.sh` starts.

| Test | What it compares |
|---|---|
| `TestDifferentialAgainstRealWireMock` | every request of the corpus (`corpus/wiremock`): status, headers, body, and the journal |
| `TestProxyingCorpusMatchesWireMock` | the proxying mappings of the corpus, with an upstream behind both servers |
| `TestAdminAPIMatchesWireMock` | the admin API: endpoints, status codes and error bodies |
| `TestWireBehaviourMatchesWireMock` | gzip, chunking, CORS, files, faults, delays and unreachable upstreams |
| `TestRecordingMatchesWireMock` | recording and snapshots: the mappings they make |
| `TestTLSMatchesWireMock` | HTTPS, HTTP/2, `--admin-api-require-https`, scheme and port matching |
| `TestBrowserProxyMatchesWireMock` | proxy mode: stubs, pass-through, failures, and the journal |
| `test/client/compat.test.mjs` | the admin API as a client library uses it; it must pass on both servers |

The command-line flags are checked by `internal/config`: every flag in WireMock's `--help` is accepted, and the
ones that change something are tested for the setting they change.

## What is not compared, and why

* **The clock, chance and addresses.** Seven corpus cases hold the current time, a random value, a generated id
  or the server's own address, or measure a date from each server's own clock (`differentialSkip` in
  `test/conformance/differential_test.go`). They are covered by unit tests with a fixed clock.
* **A listener or an upstream the test cannot share.** `c003000403` needs an HTTPS listener on 8443
  (`TestTLSMatchesWireMock` covers it) and the four `c013…` proxying cases need an upstream on port 8089
  (`TestProxyingCorpusMatchesWireMock` starts one).
* **The jwt, faker and graphql extensions.** These are separate jars that cannot run on `wiremock-standalone`
  without their dependencies and a build tool. Their behaviour was copied from the published sources of
  wiremock-jwt-extension 0.3.0, wiremock-faker-extension 0.2.0 and wiremock-graphql-extension 0.9.0, and is
  tested by unit tests and `extensions.feature`. Token signatures are random, and the faker values come from
  Shadow Mock's own word lists.
* **Not built, on purpose.** Scenarios (use `callResponses`), messaging, WebSockets and Server-Sent Events
  (WireMock 4.x beta), gRPC, and everything that is Java-only: JUnit and Spring, WAR deployment, Java
  extension points. See "Not supported" in [COMPATIBILITY.md](COMPATIBILITY.md).

## Running it yourself

You need Java 11 or later. The script downloads WireMock once, checks the jar's SHA-1, and keeps it outside
the repository.

```bash
bash scripts/wiremock-reference.sh start
SHADOWMOCK_WIREMOCK_URL=http://localhost:18080 SHADOWMOCK_WIREMOCK_CORS_URL=http://localhost:18082 \
SHADOWMOCK_WIREMOCK_TLS_PLAIN_URL=http://localhost:18083 SHADOWMOCK_WIREMOCK_TLS_URL=https://localhost:18443 \
SHADOWMOCK_WIREMOCK_PROXY_URL=http://localhost:18084 \
  go test ./test/conformance -run 'Differential|AdminAPIMatches|WireBehaviourMatches|RecordingMatches|TLSMatches|BrowserProxyMatches|ProxyingCorpusMatches|ProxyHeaderRules'
BASE_URL=http://localhost:18081 node --test test/client/compat.test.mjs
bash scripts/wiremock-reference.sh stop
```

The corpus replay needs port 8089 free, because the proxying mappings name an upstream there.

## When you find a difference

Add a case to the corpus or one of the tests above that fails on it, then fix it, or add it to
[COMPATIBILITY.md](COMPATIBILITY.md) with the reason it stays. The differential tests fail on a difference
that is in neither place.
