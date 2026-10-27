# 6. Shadow mocks

Shadow mocks are what Shadow Mock adds to WireMock. They let many tests share one mock server,
each with its own private mappings, without interfering with each other.

A shadow mock is a private set of mappings (a **layer**) that only some requests see. Everything
else still falls back to the global mappings. When a request arrives, Shadow Mock works out
which shadow mock it belongs to, tries that one first, and then falls back.

| | Key-based | URL-based |
|---|---|---|
| Selected by | A session key found in the request | The URL: `/d/<id>/...` |
| Typical use | Many tests against one shared test environment | One API under test, in CI |
| Default lifetime | 1 hour (maximum 24 hours) | 24 hours (maximum 30 days) |
| The client must | Send the key (or a match token) | Use the shadow mock's base URL |

The stack, from first to last tried, is **key-based, then URL-based, then global**.

## Key-based shadow mocks

### Create one

```bash
curl -X POST localhost:8080/__admin/sessions/key-based -d '{
  "name": "checkout-declined-card",
  "ttlSeconds": 600
}'
```

The response holds the session's `id`, its `key` and when it expires. **The key is returned only
once**, so keep it. Keys start with `smk_` and are unguessable.

```json
{ "id": "…", "kind": "key-based", "key": "smk_…", "expiresAt": "…" }
```

You can also supply `tokens`, `mappings`, `masks`, `seedFrom`, `parentId` and `metadata` at
creation. See the sections below.

### Add mappings to it

Use the same mapping calls as the global API, under the session:

```bash
curl -X POST localhost:8080/__admin/sessions/$ID/mappings -d '{
  "request":  { "urlPath": "/payments" },
  "response": { "status": 402, "body": "card declined" }
}'
```

Everything the global admin API offers has a session version at `/__admin/sessions/{id}/...`:
mappings, files, requests (the journal), counters, recordings and near misses. They take the same
bodies as the global ones.

Existing WireMock clients can use a session by sending the session key with their admin calls
in the `X-Shadow-Mock-Key` header, so only a base URL and a header change.

### Send traffic with the key

By default, put the key in the `X-Shadow-Mock-Key` header:

```bash
curl -H "X-Shadow-Mock-Key: smk_…" localhost:8080/payments
```

Only requests carrying the key see this session's mappings. A request without it gets the global
mappings, as before.

### How the key is found

Shadow Mock looks for a key (or match token) in the following order, and **stops at the first stage
that finds one**:

1. A designated **header** (default `X-Shadow-Mock-Key`). Exact lookup.
2. A designated **query parameter** (default `shadow_mock_key`). Exact lookup.
3. A designated **cookie** (default `shadow_mock_key`). Exact lookup.
4. A scan of **all header values, the query string and the URL path**.
5. A scan of the **request body**, up to a size limit and for text-like content types.

So your system under test does not have to forward a special header. If the key appears anywhere
in a header value, the URL or the body, the session is still found.

Detection cost does not grow with the number of active sessions.

### Match tokens

Sometimes the system under test does not forward your key but does forward test data, such as a
customer id. Register that data as a match token:

```bash
curl -X POST localhost:8080/__admin/sessions/key-based -d '{
  "name": "customer 99812", "tokens": ["CUST-99812"]
}'
```

This call **creates a new key-based session**, and the response contains a new generated
`id` and `key`, as in [Create one](#create-one). The token is registered on that new session, in
addition to its key. To add a token to a session that already exists, see
[Adding and removing tokens on an existing session](#adding-and-removing-tokens-on-an-existing-session) below.

Any request containing `CUST-99812` now selects this session. Tokens must be unique among
active sessions (a clash returns `409`) and at least 6 characters long by default
(`sessions.detect.minTokenLength`), to avoid false positives.

#### How a token finds its session

A token belongs to the session it was registered on. Shadow Mock keeps an index of every active
key and token, each pointing at the session that owns it. When a request contains a token, the
index gives the owning session, and the request is served through that session's mappings first.
Tokens work anywhere the key does, including the `X-Shadow-Mock-Key` header. This is also why a
token cannot belong to two sessions: the second registration would be ambiguous, so it is
refused with `409 token_in_use`.

A token stops working when its session expires or is deleted.

#### Adding and removing tokens on an existing session

You do not have to decide on every token when you create the session. Add more later with
`PATCH`, using the session's `id` (not its key):

```bash
curl -X PATCH localhost:8080/__admin/sessions/$ID -d '{ "addTokens": ["order-7731", "TEST-RUN-4471"] }'
```

Remove tokens the same way, and do both in one call if you like:

```bash
curl -X PATCH localhost:8080/__admin/sessions/$ID -d '{
  "addTokens":    ["order-8842"],
  "removeTokens": ["order-7731"]
}'
```

The call takes effect straight away: requests containing a new token select the session from then
on, and a removed token stops selecting it. The same rules apply as at creation. Each token must
be long enough and unique among active sessions. A violation fails the whole call (`409` for a
token in use, `422` for a token that is too short), so nothing in the patch is applied. Adding a
token the session already has does nothing, and only key-based shadow mocks have tokens: a
URL-based one returns `422`.

`GET /__admin/sessions/$ID` lists the session's tokens. Tokens are not secrets, so they appear in
the response, but the key never does.

#### The key itself

The session key is generated by Shadow Mock. You cannot choose it, replace it or add a second
generated key, and it is returned only when the session is created. If you lose it, create a new
session, or register a token of your own on the existing session and send that instead.
If you want to use your own identifier, such as a CI run id or a test id, as what your tests send,
register it as a token.

### If two sessions are found

If a scan finds the keys of more than one session in one request, `sessions.detect.ambiguity`
decides what happens:

| Setting | Behaviour |
|---|---|
| `first` (default) | The session whose key appears earliest in the request wins |
| `reject` | The request fails with `409` and a message saying which sessions clashed |
| `global` | All the sessions are ignored and the request uses the global mappings |

The journal records the ambiguity in every case.

## URL-based shadow mocks

### Create one

```bash
curl -X POST localhost:8080/__admin/sessions/url-based -d '{ "name": "orders-api-ci" }'
```

The response includes a `baseUrl` such as `http://localhost:8080/d/<id>`. Point the system under
test at it for its calls to the dependency. Everything after `/d/<id>` is matched as if it had been
sent to the dependency directly, so mappings are written exactly as they would be for the real
service:

```bash
curl localhost:8080/d/<id>/claims/17      # matches a mapping for /claims/17
```

A URL-based mock that does not exist, or has expired, returns a clear `404`. It never falls
back to the global mappings silently.

### Key-based inside URL-based

A key-based shadow mock can be created inside a URL-based one by giving `parentId`:

```bash
curl -X POST localhost:8080/__admin/sessions/key-based -d '{ "name": "case 7", "parentId": "<url-mock-id>" }'
```

The stack is then key-based, then the URL-based parent, then global. The child's expiry cannot
be later than its parent's.

## Precedence

* **The layer beats priority.** A priority-10 mapping in a shadow mock beats a priority-1 global
  mapping. Priority only decides between mappings in the same layer.
* If no mapping in the shadow mock matches, the request falls through to the next layer.

### Override by id

A shadow-mock mapping with the **same `id` as a global mapping** hides that global mapping for this
session, even when the shadow-mock mapping itself does not match the request. This gives
"replace" behaviour.

### Mask

A mask hides a mapping for one session without replacing it. The request then continues down the
stack as if the mapping did not exist. Mask by id, or by a request pattern:

```json
{ "masks": [ { "id": "cccccccc-0000-0000-0000-000000000001" },
             { "request": { "urlPathPattern": "/rates.*" } } ] }
```

Give masks when you create the session, or add and remove them later with `PATCH`:

```bash
curl -X PATCH localhost:8080/__admin/sessions/$ID -d '{ "addMasks": [ { "id": "cccccccc-0000-0000-0000-000000000001" } ] }'
```

## Call counts are per session

Each session has its own call counters, which start at zero when it is created. A request that
belongs to a session advances only that session's counters, **even when a global mapping serves
it**. One test's calls never change the call numbers another test sees. See
[Call-count responses](05-call-count-responses.md).

## Journals are per session

A session's journal holds only the requests that belonged to it, which makes verification
reliable on a shared server. The global journal holds every request, each tagged with the session
that served it. See [Verification](09-verification-and-debugging.md).

## Starting with mappings

Create a session with its mappings in one call:

```json
{ "name": "seeded", "mappings": [ { "request": { "urlPath": "/a" }, "response": { "status": 200 } } ] }
```

or copy them from a saved mapping set (a folder of mappings under `<root>/mapping-sets`):

```json
{ "name": "seeded", "seedFrom": { "mappingSet": "happy-path" } }
```

Later changes to the set do not affect sessions already created from it.

## Lifetime

Every session expires. When it does, its mappings, journal and call counters are discarded.

```bash
curl -X POST localhost:8080/__admin/sessions/$ID/keepalive     # extend the lifetime
curl -X PATCH localhost:8080/__admin/sessions/$ID -d '{ "ttlSeconds": 3600 }'
curl -X DELETE localhost:8080/__admin/sessions/$ID              # delete now
curl localhost:8080/__admin/sessions                            # list active sessions
```

`ttlSeconds` cannot exceed the maximum for the kind of session. Defaults and maximums are set in
the [configuration](11-configuration.md) (`sessions.defaultTtl`, `sessions.maxTtl` and the
`sessions.urlBased` equivalents). To keep sessions across restarts, set
`storage.sessions.driver: fs`.

## Seeing which layer answered

Set `respond.matchedHeader: true` to add `X-Shadow-Mock-Matched: <id>;layer=<key-based|url-based|global>`
to responses, which helps when working out why a request got the answer it did.

## A typical test

```bash
# 1. before the test: create a session and a mapping
KEY=$(curl -s -X POST localhost:8080/__admin/sessions/key-based -d '{"ttlSeconds":600}')
# (read id and key from the JSON)

curl -X POST localhost:8080/__admin/sessions/$ID/mappings -d '{
  "request":{"urlPath":"/payments"}, "response":{"status":402}}'

# 2. run the test: the system under test sends the key on its calls
curl -H "X-Shadow-Mock-Key: $KEY" localhost:8080/payments     # 402

# 3. verify what it called
curl localhost:8080/__admin/sessions/$ID/requests

# 4. clean up
curl -X DELETE localhost:8080/__admin/sessions/$ID
```

Everyone else on the server keeps getting the global answer for `/payments` the whole time.
