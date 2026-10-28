# Shadow Mock: Architecture and Design

> Status: Draft v0.2, 2026-10-26. Read with [REQUIREMENTS.md](REQUIREMENTS.md) and [SAAS.md](SAAS.md).

## 1. Design principles

1. **One engine, layered sessions.** The global session, URL-based shadow mocks and key-based shadow mocks are not three code paths. They are all `Layer`s, and a request is resolved against an ordered **layer stack**. The three modes differ only in how the stack is built.
2. **Hot path is in memory and lock-free.** Stores are the source of truth. The request path reads immutable, pre-compiled snapshots through `atomic.Pointer`. Writes build a new snapshot and swap it in.
3. **Everything is tenant-scoped from day one.** Every repository method takes a `tenant.ID` or a `store.Scope`. The exception is `BlobStore`, which takes opaque keys built by tenant-aware callers (§5). Standalone uses `tenant.Local`. Moving to SaaS changes wiring, not signatures.
4. **Pluggable edges.** Storage, auth, detection, templating and custom matchers sit behind interfaces chosen at startup by config.
5. **WireMock JSON is the wire model.** The Go structs serialise byte-compatibly with WireMock, and unknown fields are preserved.

## 2. High-level architecture

```
                    ┌──────────────────────────── shadowmock process ────────────────────────────┐
 SUT traffic ──────►│ Mock listener                                                               │
                    │   │                                                                         │
                    │   ▼                                                                         │
                    │ Router ── /<org>/__admin/* ───────────────────────► Admin API ──┐           │
                    │   │ (org from /<org-handle>, IP allowlist, /d/<id>)             │           │
                    │   ▼                                                             │           │
                    │ Session Detector (header/query/cookie → Aho-Corasick)           │           │
                    │   │                                                             │           │
                    │   ▼                                                             ▼           │
                    │ Layer-stack builder ──► Engine.Resolve ──► Responder        Auth (JWT/   │
                    │                          │     ▲              │  templating   Noop) +    │
                    │                          │     │              │  delay/fault  Authorizer │
                    │                          ▼     │              │  proxy                    │
                    │                    Compiled snapshots         ▼                           │
                    │                    (atomic.Pointer)        Journal ◄── webhooks           │
                    │                          ▲                                                │
                    │                          │ change events                                   │
                    │        Repositories (fs | memory | postgres | redis) + BlobStore           │
                    └────────────────────────────────────────────────────────────────────────────┘
Admin clients ──(JWT)──► Admin listener (may share the port with mock traffic)
```

## 3. Go module layout

```
shadowmock/
├── cmd/shadowmock/            # main: flags, config, wiring
├── internal/
│   ├── config/                # YAML/env/flags → Config
│   ├── tenant/                # tenant.ID, context helpers, org Directory, IP allowlist
│   ├── auth/                  # Authenticator, Authorizer, Principal, middleware
│   ├── model/                 # WireMock-compatible JSON structs (StubMapping, RequestPattern, ResponseDefinition…)
│   ├── match/                 # matcher compilation, evaluation, distance scoring (near misses)
│   │   ├── value/             # equalTo, contains, matches, absent, and/or/not, dates…
│   │   ├── body/              # equalToJson, jsonPath, xml, xpath, jsonSchema, multipart
│   │   └── url/               # url, urlPath, patterns, path templates
│   ├── store/                 # repository interfaces + change events
│   │   ├── fs/                # WireMock file layout (mappings/, __files/), fsnotify
│   │   ├── memory/
│   │   ├── postgres/          # SaaS: configuration, journal rows, LISTEN/NOTIFY
│   │   └── redis/             # SaaS: call counters, session registry, hot journal, fan-out, rate limits
│   ├── session/               # Session, Registry, key generation, TTL reaper
│   ├── detect/                # Detector, Aho-Corasick automaton, staged pipeline
│   ├── engine/                # Layer, Snapshot, Stack, Resolve
│   ├── counter/               # call counters per stub and request scope (call-count responses)
│   ├── respond/               # rendering, templating, delays, faults, proxy
│   │   └── template/          # Handlebars (raymond) + WireMock helpers
│   ├── journal/               # hot journal (ring buffer | Redis), batch writer, verification
│   ├── blob/                  # BlobStore: fs | memory | s3 (R2, Supabase Storage, AWS S3)
│   ├── record/                # recording/snapshot
│   ├── webhook/               # serve-event listeners
│   ├── admin/                 # /__admin handlers (WireMock-compatible + /sessions)
│   ├── server/                # listeners, router, middleware chain
│   └── telemetry/             # slog, Prometheus, OTel
├── pkg/extension/             # public interfaces for custom matchers/transformers/helpers
└── test/conformance/          # WireMock compatibility suite
```

`pkg/extension` is the only public API surface. Everything else is `internal` so it can change freely.

Today `pkg/extension` has one extension point, `RequestMatcher` (FR-2.7). A program that embeds Shadow Mock passes matchers in `app.Options.Matchers`; a mapping asks for one with `"customMatcher": {"name": "...", "parameters": {...}}`. Matchers get a copy of the request, so they cannot change what the engine matches or journals, and an unknown name rejects the mapping when it is created. Transformers and template helpers are not extensible yet.

## 4. Domain model

### 4.1 Tenancy and principal

```go
package tenant

type ID string

const Local ID = "local"

type ctxKey struct{}

func With(ctx context.Context, id ID) context.Context { return context.WithValue(ctx, ctxKey{}, id) }
func From(ctx context.Context) ID                      { /* … defaults to Local */ }

// The tenant always comes from the first path segment, /<org-handle>/…, resolved by
// tenant.Directory (section 8.1). Admin calls must also carry a JWT whose org_id matches.
// Standalone: a static directory with the single org "local"; the segment is optional.
```

```go
package auth

type Principal struct {
    UserID string    // Clerk user id or machine id
    OrgID  tenant.ID
    Roles  []Role    // org-level: viewer | editor | admin
    Scopes []string
}

type Authenticator interface {
    Authenticate(r *http.Request) (Principal, error)
}

// The engine never resolves teams. Any org member may manage sessions; global-session
// writes are made by the management app, which does the team check (SAAS.md §3.4–§3.5).

type Action string // "mappings:read", "mappings:write", "sessions:create", "global:write", ...

type Resource struct {
    Tenant  tenant.ID
    Kind    string // "global" | "session"
    ID      string
    OwnerID string
}

type Authorizer interface {
    Authorize(ctx context.Context, p Principal, a Action, res Resource) error
}
```

Implementations:

| Type | Standalone | SaaS |
|---|---|---|
| `Authenticator` | `NoopAuthenticator` returns `{UserID:"local", OrgID:"local", Roles:[admin]}` | `JWTAuthenticator` with a list of trusted Clerk issuers (session JWTs and M2M JWTs; JWKS cache; user, `org_id` and `org_role` claims mapped to roles; SAAS.md §3.2) |
| `Authorizer` | `RoleAuthorizer` (it still runs, and with the admin role always passes) | `RoleAuthorizer` plus tenant equality check plus quota checks |

The admin middleware **always** runs `Authenticate`, then puts the principal and tenant in the context. Every handler calls `Authorize` before touching a store. Because standalone uses the same chain, a missing check shows up in tests rather than in production.

### 4.2 Mappings (wire model)

```go
package model

type StubMapping struct {
    ID                    string             `json:"id,omitempty"`
    UUID                  string             `json:"uuid,omitempty"`
    Name                  string             `json:"name,omitempty"`
    Priority              *int               `json:"priority,omitempty"`
    Request               RequestPattern     `json:"request"`
    Response              ResponseDefinition `json:"response"`
    Persistent            bool               `json:"persistent,omitempty"`
    CallResponses         []CallResponse     `json:"callResponses,omitempty"` // Shadow Mock extension (FR-4.1)
    Metadata              map[string]any     `json:"metadata,omitempty"`
    PostServeActions      json.RawMessage    `json:"postServeActions,omitempty"`
    ServeEventListeners   json.RawMessage    `json:"serveEventListeners,omitempty"`

    Extra map[string]json.RawMessage `json:"-"` // unknown fields, round-tripped (custom (Un)MarshalJSON)
    InsertionIndex int64             `json:"-"`
}
```

`CallResponse` is `{calls string | every int, response ResponseDefinition}`; `calls` accepts `"3"`, `"1-2"` and `"100+"`, and is parsed into a `CallRule` at compile time (§6.3). The decoder rejects WireMock's scenario fields rather than keeping them in `Extra` (FR-4.4).

`RequestPattern` and `ResponseDefinition` mirror the WireMock schema. Matchers are a tagged union (`StringValuePattern`) decoded by inspecting the single operator key (`equalTo`, `matches`, and so on), which is how WireMock serialises them.

### 4.3 Sessions

```go
package session

type Kind string
const (
    Global    Kind = "global"
    KeyBased  Kind = "key-based"
    URLBased  Kind = "url-based"
)

type Session struct {
    ID        string     // admin-URL ID: key-based "ses_01J…"; URL-based = the 16-char URL-based mock ID
    Kind      Kind
    Tenant    tenant.ID
    Key       string     // key-based: "smk_<26 base32 chars>"; URL-based: "" (ID is in the URL)
    Tokens    []string   // extra match tokens (key-based shadow mocks only)
    ParentID  string     // key-based-inside-url-based (phase 2); "" = parent is global
    Name      string
    CreatedBy string
    CreatedAt time.Time
    ExpiresAt time.Time
    Masks     []Mask     // suppress parent mappings by ID or pattern (FR-7.8)
    Metadata  map[string]any
}

type Mask struct {
    MappingID string               `json:"id,omitempty"`
    Pattern   *model.RequestPattern `json:"request,omitempty"`
}
```

Key generation: 16 bytes from `crypto/rand`, Crockford base32, `smk_` prefix (`smk_` + 26 chars). URL-based mock IDs use the same generator with lowercase, URL-safe output and a configurable length (default 16 characters = 80 bits; see SAAS.md §2.1).

## 5. Storage abstraction (the reader pattern)

The goal is to read JSON files today and a database in SaaS, with no change outside `store/*`.

```go
package store

// Scope identifies one layer's data: the tenant plus the session ("" = global).
type Scope struct {
    Tenant    tenant.ID
    SessionID string
}

type MappingRepository interface {
    List(ctx context.Context, s Scope) ([]model.StubMapping, error)
    Get(ctx context.Context, s Scope, id string) (model.StubMapping, error)
    Put(ctx context.Context, s Scope, m model.StubMapping) error        // create or replace
    Delete(ctx context.Context, s Scope, id string) error
    DeleteAll(ctx context.Context, s Scope) error
    Watch(ctx context.Context) (<-chan ChangeEvent, error)               // fsnotify / LISTEN-NOTIFY / pubsub
}

type FileRepository interface { // __files bodies
    Open(ctx context.Context, s Scope, name string) (io.ReadCloser, error)
    Put(ctx context.Context, s Scope, name string, r io.Reader) error
    Delete(ctx context.Context, s Scope, name string) error
    List(ctx context.Context, s Scope) ([]string, error)
}

type SessionRepository interface {
    Create(ctx context.Context, sess session.Session) error
    Get(ctx context.Context, t tenant.ID, id string) (session.Session, error)
    Update(ctx context.Context, sess session.Session) error
    Delete(ctx context.Context, t tenant.ID, id string) error
    ListActive(ctx context.Context, t tenant.ID) ([]session.Session, error)
    Watch(ctx context.Context) (<-chan ChangeEvent, error)
}

// Call counters use counter.Counters (§6.3): memory in standalone, Redis in SaaS.

type JournalRepository interface {
    Append(ctx context.Context, e journal.Entry) error
    Query(ctx context.Context, s Scope, q journal.Query) ([]journal.Entry, error)
    Reset(ctx context.Context, s Scope) error
}

// BlobStore holds bulky bytes: journal bodies, __files bodies, recordings and exports.
// SaaS: S3-compatible (R2 by default). Standalone: local directory. Tests: memory.
// Keys are opaque. Callers build them: files/ and exports/ keys include the org; shared
// journal objects do not, and are reached only through org-filtered journal rows.
type BlobStore interface {
    Put(ctx context.Context, key string, r io.Reader, meta BlobMeta) error
    Get(ctx context.Context, key string, rng *ByteRange) (io.ReadCloser, error) // rng nil = whole object
    Delete(ctx context.Context, key string) error
    PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
    PresignPut(ctx context.Context, key string, ttl time.Duration, meta BlobMeta) (string, error)
}

type ByteRange struct{ Off, Len int64 }
type BlobMeta struct{ ContentType string; SHA256 []byte }

type ChangeEvent struct {
    Kind  string // "mapping" | "session" | "token" | "file" | "org" (orgs, aliases, allowlist, settings/limits)
    Scope Scope
    ID    string
    Op    string // "put" | "delete" | "reset"
}

// Provider bundles the backends, chosen per concern in config (`storage.*`):
// fs | memory | postgres | redis, plus blobs: fs | memory | s3. A composite provider mixes them.
type Provider interface {
    Mappings() MappingRepository
    Files() FileRepository
    Sessions() SessionRepository
    Counters() counter.Counters
    Journal() JournalRepository
    Blobs() BlobStore
    Close() error
}

func Open(cfg config.Storage) (Provider, error) // registry: store.Register("fs", fs.New)
```

### 5.1 Backend behaviour

| Backend | Global scope | Session scope | Watch |
|---|---|---|---|
| `fs` | `<root>/mappings`, `<root>/__files` (WireMock layout) | Memory by default, so sessions stay ephemeral. Optional `<root>/sessions/<id>/mappings` for persistence | `fsnotify`, debounced 200 ms |
| `memory` | in memory | in memory | in-process channel |
| `postgres` | `mappings(org_id, session_id NULL, id, body JSONB, …)` (SAAS.md §6.1) | same table, `session_id` set | `LISTEN/NOTIFY` via one leader, fanned out over Redis (SAAS.md §6.2) |
| `redis` | Global call counters | Call counters, session registry with TTL, hot journal | Pub/sub channel `sm:changes` |
| `s3` (`BlobStore`) | `files/<org>/…`, shared `journal/…` objects | same | – (metadata rows carry the change events) |

A **composite provider** lets standalone use `fs` for global data and `memory` for sessions and journal, which is the default.

In SaaS, `FileRepository` is implemented as a `files` metadata row (Postgres) plus a content-addressed blob (`BlobStore`). In standalone, it is the plain `__files` directory.

### 5.2 Journal tiers

```go
// journal.Writer fans every served request out to two places.
type Writer struct {
    hot  HotJournal   // Redis capped list per session/org (SaaS) | in-memory ring buffer (standalone)
    warm *BatchWriter // queue → BlobStore (bodies) + JournalRepository (rows); nil in standalone
}

type HotJournal interface {
    Append(ctx context.Context, s store.Scope, e Entry) error          // LPUSH + LTRIM + EXPIRE
    Find(ctx context.Context, s store.Scope, p match.Pattern) ([]Entry, error)
    Count(ctx context.Context, s store.Scope, p match.Pattern) (int, error)
    Reset(ctx context.Context, s store.Scope) error
}
```

* **Hot:** verification (`/requests/count`, `/requests/find`, `/requests/unmatched`) and near misses read the hot journal only. It holds full entries, so request matchers (including body patterns) run against it exactly as they do in WireMock. In SaaS it is Redis, so it is shared across instances and consistent the moment the response is sent. Its size per scope is capped (`journal.hot.maxEntries`, default 1,000 per session and 1,000 recent per org; set per plan from `orgs.limits`: Free 200, Business 5,000). Bodies over `journal.hot.maxBodyBytes` (64 KiB) are truncated and flagged. It expires with the session.
* **Warm:** `BatchWriter` flushes rows every 250 ms or 500 entries with one multi-row insert. Bodies of 4 KiB or more go into one **shared per-instance object**, written every 5 s or 8 MB. Rows pointing into it are inserted after the object write succeeds. Writes to object storage therefore don't grow with the number of orgs (SAAS.md §11.4 A). The queue is bounded; on overflow it drops entries and increments `journal_dropped_total`. It never blocks the request path.
* **Order of writes:** the object first, then the rows. A partial failure leaves only an orphan object, which lifecycle rules clean up.
* Per-org settings (`capture`, `bodies`) and plan caps are applied in `Writer` before either tier (SAAS.md §7.5).

## 6. Engine: compiled snapshots and the layer stack

### 6.1 Layer and snapshot

```go
package engine

// Snapshot is an immutable, compiled view of one layer's mappings.
type Snapshot struct {
    Scope    store.Scope
    Version  uint64
    stubs    []*CompiledStub          // sorted by (priority asc, insertion desc)
    byID     map[string]*CompiledStub
    index    *prefilter               // method + literal-path buckets to skip obvious non-matches
    masks    []CompiledMask
}

type Layer struct {
    Scope     store.Scope
    Kind      session.Kind
    snap      atomic.Pointer[Snapshot]
}

func (l *Layer) Snapshot() *Snapshot { return l.snap.Load() }
```

`CompiledStub` holds pre-compiled matchers (regexps compiled once, JSON expected values parsed once, path templates tokenised). The prefilter buckets stubs by method and by literal `url` or `urlPath`, so most requests evaluate only a small candidate set. Stubs that use patterns go into a wildcard bucket that is always evaluated.

XML bodies are matched with `antchfx/xmlquery`. `equalToXml` compares the documents as XMLUnit does by default: whitespace between elements, comments, attribute order and namespace prefixes do not matter, element order does (unless `exemptedComparisons` lists `CHILD_NODELIST_SEQUENCE`), and `enablePlaceholders` turns on `${xmlunit.ignore}`, `isNumber`, `isDateTime` and `matchesRegex(...)`, with optional delimiter regexes. `matchesXPath` matches when the expression selects something, or when what it selects matches the sub-matcher; `xPathNamespaces` maps the expression's prefixes to namespace URIs, and the document's elements are renamed to those prefixes before the expression runs, so the document's own prefixes need not agree.

A `LayerManager` holds `map[store.Scope]*Layer`. It subscribes to `Watch` events, rebuilds the affected layer's snapshot off the hot path, and swaps it in with an atomic store.

### 6.2 Stack resolution

```go
type Stack []*Layer // index 0 = most specific (key-based), last = global

type Result struct {
    Stub     *CompiledStub
    Layer    *Layer
    NearMiss []NearMiss // populated only on no-match
}

func (e *Engine) Resolve(ctx context.Context, req *Request, stack Stack) Result {
    hidden := maskSet{} // IDs hidden by more-specific layers (overrides + masks)
    for _, layer := range stack {
        snap := layer.Snapshot()
        for _, cs := range snap.candidates(req) {
            if hidden.has(cs.ID) || hidden.matchesPattern(req) {
                continue
            }
            if cs.Match(req).Matched {
                return Result{Stub: cs, Layer: layer}
            }
        }
        hidden.addIDs(snap.byID)  // FR-7.7: same ID in a specific layer hides the parent's
        hidden.addMasks(snap.masks) // FR-7.8
    }
    return Result{NearMiss: e.nearMisses(req, stack, hidden)}
}
```

Key rules:

* **The layer beats priority** (FR-7.5): the outer loop is the layer, and priority ordering applies only within a snapshot.
* **Override by ID**: a key-based or URL-based shadow mock stub with ID X hides global stub X even if that copy does not match this request. That is what "replace this mapping" means.
* `Request` is a parsed view (method, URL parts, headers, cookies, query, body bytes, lazily parsed JSON and XML) built **once** and shared across layers.
* **Matching never depends on state.** Resolution is a pure function of the request and the snapshots. Call counts only choose **which response** the selected stub returns (§6.3), so they are read after matching, never during it.

### 6.3 Call-count responses (FR-4.1 to FR-4.4, FR-7.6)

WireMock scenarios are not supported. In their place, a stub can pick its response by call number.

```go
package counter

// Key identifies one counter: who is calling (the request's most specific scope) and
// which stub is serving (the stub's own scope and id). Including the stub's scope keeps
// a session override and the global stub it hides on separate counters.
type Key struct {
    RequestScope store.Scope // the session the request was attributed to, or global
    StubScope    store.Scope // the layer the serving stub belongs to
    StubID       string
}

type Counters interface {
    Next(ctx context.Context, k Key) (int64, error)            // atomic increment, returns 1, 2, 3, …
    List(ctx context.Context, s store.Scope) ([]Entry, error)   // counters whose RequestScope is s
    Set(ctx context.Context, k Key, n int64) error
    Reset(ctx context.Context, s store.Scope, stubID string) error // stubID "" = all in scope
}
```

```go
// CompiledStub keeps its call rules pre-parsed.
type CallRule struct {
    From, To int64             // inclusive; To = 0 means open-ended ("100+")
    Every    int64             // > 0 for "every n"
    Response *CompiledResponse
}

// ResponseFor returns the response for call n: first matching rule, else the default.
func (cs *CompiledStub) ResponseFor(n int64) *CompiledResponse {
    for _, r := range cs.CallRules {
        if r.Every > 0 && n%r.Every == 0 { return r.Response }
        if r.Every == 0 && n >= r.From && (r.To == 0 || n <= r.To) { return r.Response }
    }
    return cs.Response
}
```

* **The counter is only touched when needed.** After `Resolve` selects a stub, the engine calls `Counters.Next` only if the stub has `callResponses` or its templates use `request.callNumber`. Most stubs never touch a counter.
* **Scope:** `RequestScope` is the most specific layer in the request's stack:
  * The session, for key-based and URL-based traffic. A key-based shadow mock nested in a URL-based one has its own counters.
  * Global, for plain traffic.

  A new session therefore starts at zero, with nothing copied (FR-7.6).
* **Lifetime:** counters for a session are deleted with the session. Replacing or deleting a stub resets its counters. In standalone, an in-memory `map[Key]*atomic.Int64` is used. In SaaS, a Redis `INCR` on `cnt:{org}:{requestScope}:{stubScope}:{stubID}` gives atomic, gap-free numbers across instances, with the session's TTL applied to its keys.
* **Rejected scenario fields:** the mapping decoder rejects `scenarioName`, `requiredScenarioState` and `newScenarioState` with `scenarios_not_supported`. They are deliberately **not** kept in `Extra`, so they can never be silently ignored (FR-4.4).
* **Recording:** `repeatsAsCallResponses` folds identical requests that got different responses into one stub, with `calls` rules in recorded order.

## 7. Session detection

### 7.1 Pipeline

```go
package detect

type Hit struct {
    SessionID string
    Stage     Stage  // Header | Query | Cookie | ScanMeta | ScanBody
    Token     string // which key/token matched (logged redacted)
    Offset    int
}

type Detector interface {
    Detect(ctx context.Context, t tenant.ID, req *engine.Request) (Hit, bool, error)
}
```

The staged implementation stops at the first stage that produces a hit:

| # | Stage | Mechanism | Cost |
|---|---|---|---|
| 1 | Designated headers (`X-Shadow-Mock-Key`, configurable list) | `map[string]sessionID` lookup on the value | O(1) |
| 2 | Designated query params (`shadow_mock_key`) | map lookup | O(1) |
| 3 | Designated cookie | map lookup | O(1) |
| 4 | Metadata scan: all header values, raw query string and URL path, joined with `\x00` separators | Aho-Corasick | O(n) |
| 5 | Body scan: first `maxScanBytes` of the body (content-type allow-list, optional gunzip) | Aho-Corasick | O(n) |

Stages 1 to 3 are the fast path for well-behaved clients. Stages 4 and 5 handle a SUT that forwards the key indirectly, for example inside a JSON field, a correlation ID, a `traceparent` baggage item, or a match token such as a test customer number.

### 7.2 Aho-Corasick automaton

```go
type Automaton interface {
    // FindAll returns non-overlapping, leftmost-longest matches.
    FindAll(haystack []byte) []Match // Match{PatternIndex, Start, End}
}

type tenantIndex struct {
    exact     map[string]string // key/token → sessionID (stages 1–3)
    ac        Automaton         // over all keys + tokens (stages 4–5)
    patterns  []string          // PatternIndex → token
    owners    []string          // PatternIndex → sessionID
    version   uint64
}

type Index struct {
    byTenant sync.Map // tenant.ID → *atomic.Pointer[tenantIndex]
}
```

* **Library.** Use `github.com/petar-dambovaliev/aho-corasick` (a port of the Rust `aho-corasick` crate, with DFA and leftmost-longest support), wrapped behind the `Automaton` interface so it can be swapped for an in-house implementation if needed.
* **Per-tenant automaton.** Keys are matched only within the tenant resolved for the traffic (FR-9.6). Standalone has one automaton.
* **Rebuild.** Session create, delete, token change or expiry marks the index stale. The next lookup rebuilds it once, copy-on-write, and swaps it in atomically, so a burst of changes costs one rebuild and a session is detectable the moment its create call returns (inside the 100 ms of NFR-2.4). Build cost is O(total pattern length), which takes milliseconds even for 10k sessions. A debounced background builder (25 ms) replaces this when the first lookup after a large change becomes a latency problem.
* **Fast negative.** If a tenant has no active sessions, stages 1 to 5 are skipped entirely (`ac == nil`).
* **Ambiguity** (FR-7.10). Collect all distinct `owners` across matches in the stage, then apply the policy (`first` by lowest `Start`, or `reject`, or `global`).
* **False-positive guard.** Tokens have a minimum length. Keys carry the `smk_` prefix and 26 random characters, so a false positive on a key is effectively impossible.

### 7.3 Body handling

The body is read once into a pooled buffer, capped at `maxBodyBytes` (hard limit, 413 above it). That buffer is reused for detection, matching, templating, the journal and proxying (`io.NopCloser(bytes.NewReader(buf))`). For bodies larger than `maxScanBytes`, only the prefix is scanned, and the journal notes `scanTruncated=true`.

## 8. Request lifecycle (mock traffic)

```
1. server.Router
   a. "/__health", "/__ready"              → health (no org); standalone also "/__admin/health"
   b. Org segment: "/<org-handle>/…"       → tenant = OrgDirectory.Lookup(handle)  (in-memory; aliases included)
        standalone: segment optional; if absent tenant = Local, if present it must be "local"
        unknown handle → 404
   c. Rest of path, after the org segment:
        "__admin/…"     → admin pipeline (section 10)          (JWT + org match)
        "d/<id>/…"      → urlMockID = id (must belong to tenant); strip prefix
        anything else   → global/key-based traffic; strip "/<org-handle>"
      Unknown or expired id → 404 (FR-8.6), never falls through
   d. IP allowlist: if tenant's list is non-empty and clientIP ∉ list → 403 ip_not_allowed
      (clientIP from server.clientIp; see section 8.1). Not counted as a billable request
   e. Rate limit (org token bucket, from orgs.limits) → 429 if exceeded. Not billable
   f. Count the request (in-memory meters, flushed to Redis every second)
2. req := engine.ParseRequest(r, limits)          // single body read
3. hit := Detector.Detect(tenant, req)            // key-based shadow mock (scoped to the URL-based shadow mock's children if nested)
4. stack := build:  [key-based?] + [url-based?] + [global(tenant)]
5. res := Engine.Resolve(req, stack)
6. if res.Stub == nil → 404 with near-miss body (WireMock format)
   else → if the stub has callResponses or uses request.callNumber:
              n = Counters.Next(Key{requestScope, stubScope, stubID}); resp = res.Stub.ResponseFor(n)
          respond.Render(resp, req, templateCtx{layer, session, callNumber: n})   // gzip if accepted
7. journal.Append(Entry{tenant, sessionID, layerKind, stubID, req, resp, timings, detectHit})
8. webhooks fire asynchronously
```

### 8.1 Org directory and IP allowlist

```go
package tenant

type Org struct {
    ID          ID
    Handle      string
    Allowlist   []netip.Prefix // empty = allow all
    AllowAdmin  bool           // apply the allowlist to /__admin too
    Plan        string
    Limits      Limits         // from orgs.limits: rps, sessions included/ceiling, hot journal caps, retention, CI keys
    Settings    Settings       // from orgs.settings: journal capture/bodies, gzip, detection options
}

// Directory resolves handles (and aliases) to orgs. It is an in-memory snapshot,
// refreshed by change events on orgs / org_handle_aliases / org_ip_allowlist.
type Directory interface {
    Lookup(handle string) (*Org, bool)
}

func (o *Org) Allows(ip netip.Addr) bool {
    if len(o.Allowlist) == 0 { return true }
    for _, p := range o.Allowlist { if p.Contains(ip) { return true } }
    return false // switch to a prefix trie if lists exceed ~256 entries
}
```

* **Client IP extraction** (`server.ClientIP`), configurable per host:
  * `trustedHops: N` (the Render default is 1): take the N-th entry from the right of the configured header (`X-Forwarded-For`).
  * `trustedProxies: [CIDRs]`: walk the header from the right, skipping addresses that are trusted proxies.
  * `header: Fly-Client-IP` style: a single header set by the platform.
  * Nothing configured (standalone): use the TCP peer address.

  IPv4-mapped IPv6 addresses are unmapped before matching.
* The check runs **before** the body is read, so rejected callers cost almost nothing.
* Standalone: the directory holds a single `local` org with an empty allowlist. An allowlist can still be set in config (`tenancy.local.ipAllowlist`) for a central server on a shared network.

## 9. Response rendering

* `respond.Renderer` handles status, headers, the body source (inline, JSON, base64 or file via `FileRepository` with the **layer's scope first, then global**, so sessions can override `__files` too), and templating.
* **Templating.** `respond/template` wraps `github.com/mailgun/raymond/v2` (a maintained fork of raymond, Handlebars for Go) and registers the WireMock helper set. Parsed templates are cached by source text. Three things differ from stock Handlebars, to match WireMock: output is not HTML-escaped (values are marked safe, and lists and objects are unescaped after rendering through `template.Mark`); numeric path segments such as `request.query.tag.0` and the assigning forms `{{parseJson body 'x'}}` and `{{regexExtract s re 'x'}}` are rewritten to plain Handlebars before parsing; and raymond fixes each helper's argument count, so helpers take WireMock's documented arguments with options in the hash. Templates read only the data they are given, so there is no file or network helper (NFR-5.3). A stub is templated when it lists the `response-template` transformer or `templating.global` is on. A stub whose response mentions `callNumber` also takes a call number from the counter.
* **Webhooks.** `serveEventListeners` and `postServeActions` entries named `webhook` are decoded with the mapping. After the response is written, `webhook.Dispatcher` sends each one in the background: it waits out the delay, renders the URL, headers and body with `originalRequest`, `response` and `parameters`, and calls through `egress.Client`. Failures are logged and never change the served response. With `egress.blockPrivate` the client refuses loopback, private and link-local addresses, after DNS resolution, and does not follow redirects.
* **Proxying.** A response with `proxyBaseUrl` is forwarded by `proxy.Forwarder` through the egress client: the request's path (less `proxyUrlPrefixToRemove`) and query are appended to the base URL, hop-by-hop headers are dropped, `removeProxyRequestHeaders` and `additionalProxyRequestHeaders` are applied, and the upstream answers uncompressed. The base URL is a template when the response is templated. Redirects are passed on, not followed. The mapping's delays and gzip setting apply to the relayed response. A failed forward is a 502 with `bad_gateway`, or `egress_blocked` when the destination is refused.
* **Recording.** `record.Recorder` holds at most one recording per scope (global, or one shadow mock). While it runs, a request that no mapping answers is proxied to `targetBaseUrl` by the most specific scope in the request's stack that is recording, and the exchange is kept if it passes `filters`. Stopping builds one mapping per distinct request (`record.Build`): `url`, optionally captured headers and a body pattern, and the response as `jsonBody`, `body` or `base64Body`. With `repeatsAsCallResponses`, repeated requests become one mapping whose `callResponses` give each call its recorded response, in place of WireMock's scenarios. `persist` (default true) stores the mappings in the scope. A snapshot builds the same mappings from proxied entries in the journal.
* **Compression.** Responses are gzip-compressed when the client sends `Accept-Encoding: gzip`, unless the mapping sets `"gzipDisabled": true`, the org setting turns it off, the response already has a `Content-Encoding`, or the body is under 1 KiB. This matches WireMock's default and is the main lever on outbound bandwidth cost (SAAS.md §11.4 C).
* **Delays.** `time.Timer` honouring the request context, so a cancelled client frees the goroutine.
* **Faults.** Use `http.Hijacker` to reset the connection, close it empty, or write garbage chunks.
* **Proxy.** `httputil.ReverseProxy` per target (pooled transport), applying header add and remove rules. In record mode, the response is captured to the active recording's scope.

## 10. Admin API

The full contract is in [api/openapi.yaml](../api/openapi.yaml) (OpenAPI 3.1): every WireMock-compatible endpoint, its session-scoped twin, the session and runtime extensions, platform endpoints and error codes. The CI token exchange served by the management app is in [api/ci-token.openapi.yaml](../api/ci-token.openapi.yaml). This section describes how requests flow through the engine.

### 10.1 Pipeline

```
/<org-handle>/__admin/… → tenant from the path (Directory.Lookup)
             → IP allowlist, if the org has applyToAdmin on
             → Authenticate (Noop | JWT)  → Principal{UserID, OrgID, Roles}
             → OrgID == tenant.ID ?  else 403 org_mismatch
             → scope resolution:
                 /__admin/sessions/{id}/<wiremock-path>   → Scope{tenant, id}
                 /__admin/<wiremock-path> + X-Shadow-Mock-Key: <key|id> → Scope{tenant, id}
                 /__admin/<wiremock-path>                 → Scope{tenant, ""} (global)
             → Authorize(principal, action, resource)   (incl. admin.globalWrites)
             → WireMock-compatible handler (same code for every scope)
```

Because WireMock handlers are written once against a `Scope`, every WireMock endpoint works per session for free. The org-match check is in the shared admin middleware, so no handler can skip it. In standalone mode `NoopAuthenticator` returns org `local`, which always matches. The central server uses `TokenAuthenticator` (`auth.driver: token`): bearer tokens from configuration, each with a role, held only as SHA-256 hashes and compared in constant time against every stored token. The same Authorizer runs, so the roles carry over to the hosted service unchanged. `mode: server` refuses to start without it, and the metrics endpoint requires a viewer token when it is on. Mock traffic and the health endpoints are never authenticated.

**Auth summary:** everything under `/<org-handle>/__admin/` requires a JWT whose org matches the path. All other traffic under `/<org-handle>/` (mock calls, any HTTP method) requires no JWT and is limited only by the IP allowlist and quotas.

### 10.2 Session endpoints

| Method and path | Purpose |
|---|---|
| `POST /__admin/sessions/key-based` | Create. Body: `{name, ttlSeconds, tokens[], mappings[], masks[], parentId?}`. Returns `{id, key, expiresAt}` |
| `POST /__admin/sessions/url-based` | Create. Body: `{name, ttlSeconds, mappings[], masks[]}`. Returns `{id, baseUrl, expiresAt}` |
| `GET /__admin/sessions` | List the tenant's active sessions (filter by kind) |
| `GET/PATCH/DELETE /__admin/sessions/{id}` | Inspect, extend TTL, add tokens or masks, or delete |
| `POST /__admin/sessions/{id}/keepalive` | Extend the TTL by its original duration |
| `* /__admin/sessions/{id}/mappings…` | WireMock mappings API, session-scoped |
| `* /__admin/sessions/{id}/requests…` | Journal and verification, session-scoped |
| `GET /__admin/sessions/{id}/counters`, `POST …/counters/reset`, `PUT/DELETE …/counters/{stubMappingId}` | Call counters, session-scoped (FR-4.3) |
| `* /__admin/sessions/{id}/near-misses…` | Near misses, session-scoped |
| `POST /__admin/sessions/{id}/recordings/start` | Record into the session |

Shadow Mock extensions:

| Method and path | Purpose |
|---|---|
| `GET /__admin/counters`, `POST /__admin/counters/reset`, `PUT/DELETE /__admin/counters/{stubMappingId}` | Global call counters: list, reset all, set or reset one (FR-4.3) |
| `GET /__admin/scenarios…` | Always `404 not_supported` (FR-4.4) |
| `POST /__admin/mappings/validate` | Validate a mapping against the engine's own parser and matchers |
| `POST /__admin/mappings/dry-run` | Resolve a sample request against a scope and return what would be served |
| `GET /__admin/stats/live` | Live counters for the org |
| `GET /__admin/usage` | Current-period usage against plan limits |

TTLs: key-based sessions default to 1 h (max 24 h), and URL-based to 24 h (max 30 days) (FR-7.2). A TTL reaper (ticking every second, driven by a min-heap of expiries) deletes expired sessions. Deletion cascades through the repositories and emits change events, so the layers and the detector drop them. In SaaS the reaper runs only on the leader instance (the same Redis lease as the `LISTEN` leader), and Redis key TTLs make expiry immediate on every instance.

### 10.3 Example test flow (key-based shadow mock)

```bash
MOCK=https://shadowmock.kaizentis.xyz/my-org     # standalone: http://localhost:8080
AUTH="Authorization: Bearer $SHADOW_MOCK_JWT"     # required for every /__admin call; omitted below for brevity

# 1. Test creates its session
RESP=$(curl -s -XPOST $MOCK/__admin/sessions/key-based \
  -d '{"name":"checkout-declined-card","ttlSeconds":600,"tokens":["CUST-99812"]}')
KEY=$(echo "$RESP" | jq -r .key); ID=$(echo "$RESP" | jq -r .id)

# 2. Test adds an override
curl -XPOST $MOCK/__admin/mappings -H "X-Shadow-Mock-Key: $KEY" -d '{
  "request":  {"method":"POST","urlPath":"/payments"},
  "response": {"status":402,"jsonBody":{"error":"card_declined"}}}'

# 3. Test drives the SUT, passing the key (header, or just using CUST-99812 in the payload)
curl -XPOST $SUT/checkout -H "X-Shadow-Mock-Key: $KEY" -d '{"customer":"CUST-99812"}'
#    SUT → mock /payments: the key-based shadow mock override is served; other calls fall through to global.

# 4. Verify, then clean up
curl $MOCK/__admin/sessions/$ID/requests/count -d '{"method":"POST","urlPath":"/payments"}'
curl -XDELETE $MOCK/__admin/sessions/$ID
```

## 11. Configuration

```yaml
mode: standalone            # standalone | server
listen:
  mock:  ":8080"
  admin: ""                 # empty = same listener as mock, under adminPrefix
adminPrefix: /__admin
admin:
  globalWrites: allow       # allow (standalone) | deny (SaaS: global edits only via the management app)
root: ./wiremock            # mappings/ and __files/
storage:
  global:   { driver: fs, watch: true }
  sessions: { driver: memory }
  journal:
    hot:  { driver: memory, maxEntries: 10000, maxBytes: 536870912 }  # standalone keeps bodies whole and drops the oldest entries past the budget. SaaS: redis, maxEntries 1000 per scope (plan-dependent), maxBodyBytes 65536
    warm: { driver: none }   # SaaS: postgres, rowBatchMs: 250, rowBatchSize: 500, inlineMaxBytes: 4096,
                             #       bodyObjectFlushMs: 5000, bodyObjectMaxBytes: 8388608
  counters: { driver: memory }    # SaaS: redis
  blobs:
    driver: fs                    # fs | memory | s3
    dir: ./blobs
    # s3: { endpoint: https://<account>.r2.cloudflarestorage.com, bucket: shadowmock, region: auto }
respond:
  gzip: auto                # auto = gzip when the client sends Accept-Encoding: gzip (WireMock default) | off
auth:
  driver: noop              # noop | jwt
  issuers: []               # SaaS: clerk-users and clerk-ci entries, see SAAS.md §3.2
  # - name: clerk-users
  #   jwksUrl: https://clerk.kaizentis.xyz/.well-known/jwks.json
  #   issuer: https://clerk.kaizentis.xyz
  #   audience: shadowmock
  #   userClaim: sub
  #   orgClaim: org_id
  #   rolesClaim: org_role
  #   roleMap: { "org:admin": admin, "org:member": editor, "org:viewer": viewer }
tenancy:
  directory: static         # static (standalone: single org "local") | postgres (SaaS: orgs + aliases + allowlists)
  requireOrgSegment: false  # true in SaaS: every path starts with /<org-handle>
  local:
    ipAllowlist: []         # standalone/central server only, e.g. ["10.0.0.0/8"]
server:
  clientIp:
    header: ""              # Render: X-Forwarded-For; Fly.io: Fly-Client-IP; empty = TCP peer
    trustedHops: 0          # Render: 1 (verify with a test request before relying on the IP allowlist)
    trustedProxies: []      # alternative to trustedHops: CIDRs of trusted proxies
sessions:
  defaultTtl: 1h            # key-based
  maxTtl: 24h               # key-based
  keyPrefix: smk_
  detect:
    headers: [X-Shadow-Mock-Key]
    queryParams: [shadow_mock_key]
    cookies: [shadow_mock_key]
    scanMetadata: true
    scanBody: true
    maxScanBytes: 1048576
    contentTypes: ["text/*", "application/json", "application/*+json", "application/xml", "application/x-www-form-urlencoded"]
    decompress: true
    minTokenLength: 6
    ambiguity: first        # first | reject | global
  urlBased:
    pathPrefix: /d          # after the org segment: /<org-handle>/d/<id>/…
    idLength: 16
    defaultTtl: 24h
    maxTtl: 720h            # 30 days
limits:
  maxBodyBytes: 10485760
  # Opt-in quotas, all off at 0: maxSessions, maxMappings, maxMappingsPerSession, maxDelay, maxConnections, requestsPerSecond, burst
templating: { global: false }
egress: { blockPrivate: false }   # SaaS sets true: no webhooks or proxying to private addresses
```

## 12. Concurrency model

| Component | Read path | Write path |
|---|---|---|
| Layer snapshots | `atomic.Pointer` load, no locks | Rebuild off-path, then an atomic store |
| Detector index | `atomic.Pointer` load per tenant | Debounced rebuild, then an atomic store |
| Call counters | Standalone: `atomic.Int64` per key in a `sync.Map`. SaaS: Redis key per counter | Atomic increment (`Add` / `INCR`) |
| Journal | Standalone: ring buffer. SaaS: Redis hot list per scope | Append / pipelined `LPUSH`+`LTRIM`; warm tier batched off-path |
| Session registry | `sync.Map` id → session | Repository write, then a change event |

There is no global lock on the request path, so key-based shadow mocks scale with the number of tests.

**Metrics.** `internal/metrics` owns a private Prometheus registry (`prometheus/client_golang`), so two engines in one process never share counters. `mock.Service.finish` reports every served mock request once, through the small `mock.Observer` interface: method, status, the layer that served it and the detection stage, if any. The detector reports each rebuild (pattern count and duration, timed with the injected clock) through `detect.Index.Observe`, and the active-session gauge reads the registry when scraped. The package's methods take only the permitted label values (org, method, status, layer, stage), so a session id, path or client address cannot become a label by accident; a unit test and an acceptance scenario check the exposition. `/__metrics` is served on the admin listener when there is one, and not on the mock listener.

**Tracing and logs.** `internal/telemetry` starts a server span for each mock request (in `mock.Service.ServeMock`) and each admin call (in the router), under the trace in a caller's W3C `traceparent` header, and exports them over OTLP/HTTP. The span is ended in `finish`, where the layer, mapping, session and status are known. The log handler adds `trace_id` and `span_id` to any record logged with a context that carries a span. Spans and the `request served` line hold the path and never the query. Proxied calls inject the trace context. Tracing is off by default; the types are nil-safe, so the engine runs the same code either way.

## 13. Moving to SaaS (what changes)

The full SaaS platform context (Next.js and Clerk on Vercel, the separate management app, Supabase Postgres, the engine on Render with Render Key Value, and R2 object storage) is in [SAAS.md](SAAS.md). Engine-side summary:

| Concern | Standalone | SaaS |
|---|---|---|
| Auth | `NoopAuthenticator` | `JWTAuthenticator` only, trusting Clerk session JWTs (users) and Clerk M2M JWTs (CI) |
| Tenancy | Static directory, single org `local`, org segment optional | Postgres-backed directory: `shadowmock.kaizentis.xyz/<org-handle>/…`, admin JWT org must match |
| Network | optional `local.ipAllowlist` | Per-org IP allowlist from the management app |
| Storage | `fs` + `memory` | `postgres` shared with the management app; bodies and `__files` in S3-compatible object storage (R2) |
| Change events | fsnotify, in-process | Postgres triggers → `pg_notify` → one listener → Redis pub/sub → all instances, plus 30 s reconciliation |
| Call counters, hot session registry | memory | Redis-compatible shared cache (atomic `INCR`, TTL per session) |
| Journal and stats | ring buffer | Hot: Redis per session and org (verification). Warm: batched lean Postgres rows plus shared body objects. Counters in memory → Redis every second, rolled up per minute |
| Quotas and metering | none | Plan limits from `orgs.limits` (rps, sessions, hot journal, retention, CI keys) via middleware and `Authorizer`; meters to `usage_daily` |
| Hosting | binary or Docker | Render web service (autoscaled, min 2), Render Key Value on the private network, Supabase Postgres, R2. The same Docker image runs anywhere (Fly.io as the fallback for true TCP faults) |

None of these changes touch `engine`, `match`, `detect` or `admin` handler code. They are new implementations of existing interfaces plus wiring in `cmd/`.

## 14. Testing strategy

* **Unit tests:** each matcher, the template helpers, the detector stages and ambiguity policies, call-rule parsing and selection (`ResponseFor`), rejection of scenario fields, and the stack resolution rules (layer beats priority, override by ID, masks).
* **Conformance suite** (`test/conformance`): WireMock's documented mapping examples, each run as request → expected response. The differential test (`differential_test.go`) replays the same corpus against a real WireMock (3.13.2) and compares replies and journals; `test/client` holds a Node suite that must pass on both servers. Both run in the `wiremock-reference` CI job, and every tolerated difference is listed in [COMPATIBILITY.md](COMPATIBILITY.md).
* **Concurrency tests:** many parallel key-based shadow mocks calling the same stub with `callResponses`. Assert each session sees gap-free call numbers starting at 1, under `-race`.
* **Benchmarks:** `Resolve` with 5k mappings, and `Detect` with 1 KiB, 100 KiB and 1 MiB bodies against 10, 1k and 10k active sessions (checks NFR-2.1 and NFR-2.2).
* **Fuzzing:** `go test -fuzz` on mapping JSON decoding, the body scanner and template parsing.

## 15. Delivery phases

| Phase | Scope |
|---|---|
| **P0 – Core** | Model and JSON compatibility, `fs` and `memory` stores, URL, header, query and body matchers (equalTo, contains, regex, equalToJson, jsonPath), basic responses, admin mappings, requests and reset, auth interfaces with Noop, single global layer through the stack engine |
| **P1 – Shadow** | Session registry and TTL, detection stages 1 to 3, then the Aho-Corasick metadata and body scans, session-scoped admin, call-count responses with counters per session, scenario-field rejection, override and mask |
| **P2 – URL-based shadow mocks** | `/d/<id>` path-prefix routing, nested key-based shadow mock inside a URL-based shadow mock |
| **P3 – Parity** | Templating, delays and faults, gzip responses, proxy, record and playback, XML and XPath, JSON schema, multipart, webhooks, near misses, conformance suite |
| **P3.1 – Parity completion** | Everything in PARITY_PLAN.md: Jayway JSONPath, template blocks and helpers, admin API shapes and filters, wire behaviours, command-line flags, recording shape, TLS and HTTP/2, browser proxy, extension replacements (FR-10.1 to FR-10.16) |
| **P4 – Central server** | Separate admin listener, metrics and tracing, Docker, hardening, quotas |
| **P5 – SaaS** | Clerk and machine JWT auth, org-handle path tenancy with JWT org match, per-org IP allowlist, usage metering and quotas, Postgres (shared schema, migrations, JSON Schema export) and Redis, distributed change events, async journal and stats, validate and dry-run endpoints for the management app |

## 16. Key decisions (ADR summary)

1. **Layer beats priority.** It is predictable for test authors: "my session's mapping wins if it matches".
2. **Override by ID hides even if not matching.** It gives "replace" semantics consistent with `PUT /mappings/{id}`.
3. **Call-count responses instead of scenarios.** Matching never depends on state. A counter per stub per request scope only selects the response, so sessions start at zero, nothing is forked, and a single atomic increment is the only per-request state operation.
4. **Detection stops at the first stage with a hit.** Explicit signals (headers) outrank incidental ones (body text).
5. **Per-tenant Aho-Corasick automaton, rebuilt copy-on-write.** Lookup cost is independent of session count, with no locks on reads.
6. **Store is never read on the hot path.** It keeps a database-backed SaaS fast and makes the store swappable.
7. **Auth chain runs in standalone too, with Noop.** It prevents auth being bolted on later.
8. **The org is always in the path, and admin JWTs must match it.** The org is known before detection, so there is no cross-tenant index, and a token for one org can never act on another.
9. **Admin needs a JWT; mock traffic never does.** Mock traffic is protected by the per-org IP allowlist instead, checked before the body is read.
