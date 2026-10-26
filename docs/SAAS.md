# Shadow Mock on the Kaizentis SaaS Platform

> Status: Draft v0.2, 2026-10-26. Background for the Go mock engine. Read with [REQUIREMENTS.md](REQUIREMENTS.md) (requirements and decisions log) and [DESIGN.md](DESIGN.md) (engine design).

**Contents:** 1 Overview · 2 Addressing · 3 Identity and access · 4 IP allowlist · 5 Management app vs engine · 6 Data · 7 Journal · 8 Object storage · 9 Hosting · 10 Stats and metering · 11 Cost model · 12 Pricing and billing · 13 Verify before launch · 14 Engine impact · Appendix A: alternatives considered

## 1. Overview

Kaizentis (`kaizentis.xyz`) is a multi-application SaaS platform, and Shadow Mock is one of its applications. Clerk is the identity provider for the whole platform, and a Clerk **organisation** is the Shadow Mock **tenant**.

### 1.1 Components

| Component | Tech | Hosting | Role |
|---|---|---|---|
| **Platform shell** | Next.js + Clerk | Vercel | Sign-up and sign-in, orgs, **teams**, billing and subscriptions, app catalogue, launching each app's management app |
| **Shadow Mock management app** | Next.js + Clerk | Vercel | UI for mappings, sessions, journals, stats and settings. Writes configuration **directly to the shared database**. Hosts the CI token exchange |
| **Shadow Mock engine** (this Go app) | Go, no UI | **Render** web service, autoscaled, ≥ 2 instances (§9.1) | Serves mock traffic and the runtime and admin API |
| **Database** | PostgreSQL | **Supabase** (§9.3) | Source of truth for orgs, mappings, sessions, file metadata, journal rows, stats and usage |
| **Shared cache** | Valkey (Redis-compatible) | **Render Key Value**, private network (§9.2) | Scenario state, session registry, hot journal, change fan-out, rate limits, live counters |
| **Object storage** | S3 API | **Cloudflare R2** (§8) | Journal bodies, `__files` bodies, recordings and exports |
| **Ops observability** | Prometheus metrics, OTel traces, logs | Grafana Cloud, Better Stack or Axiom (free or low tier) | Running the platform. **Not** the customer journal |

### 1.2 Topology

```
                     Vercel (kaizentis.xyz)
 Users ────────────► Platform shell ─────── Clerk (users, orgs, M2M tokens)
                     Shadow Mock management app
                       ├── SQL (transaction pooler, RLS) ─────────────────────► Supabase Postgres
                       ├── runtime API calls (Clerk JWT) ──────┐                    ▲     │
                       └── /api/ci/token: CI key → Clerk JWT   │       SQL + LISTEN │     │ NOTIFY
                                                               ▼  (session pooler)  │     ▼
 CI / tests ──── /<org>/__admin/… (JWT) ───────────► Engine on Render (Go, ≥ 2) ────┘
 SUT ─────────── /<org>/…  and  /<org>/d/<id>/… ───►     │                 │
                                                          │ private net     │ S3 API
                                                          ▼                 ▼
                                                  Render Key Value     Cloudflare R2
```

## 2. Addressing

### 2.1 URL scheme

Every engine URL starts with the org's handle: `https://shadowmock.kaizentis.xyz/<org-handle>/…`. There is one host, no per-tenant DNS, and the org is always known from the first path segment.

| Traffic | Example | Auth |
|---|---|---|
| Global session | `/my-org/claims/123` | None; IP allowlist applies (§4) |
| Key-based shadow mock | `/my-org/claims/123` + a session key in the request | None; IP allowlist |
| URL-based shadow mock | `/my-org/d/<urlMockId>/claims/123` | None; IP allowlist |
| Key-based inside URL-based | `/my-org/d/<urlMockId>/…` + session key | None; IP allowlist |
| Admin and runtime API | `/my-org/__admin/…` | **JWT** whose org matches the handle (§3.3) |
| Platform health (load balancer only) | `/__health`, `/__ready` | None; not org-scoped |

* The `/<org-handle>` and `/<org-handle>/d/<id>` prefixes are **stripped before matching**, so mappings are written exactly as for the real dependency (`/claims/...`).
* WireMock clients work unchanged with the base URL `https://shadowmock.kaizentis.xyz/my-org`, because they append `/__admin/...` to it.
* An unknown handle, or an unknown or expired URL-based mock ID, returns 404 and never falls through to another org. A session key belonging to another org is ignored, because session detection only uses the org's own index (keys and match tokens).
* URL-based mock IDs default to 16 lowercase base32 characters (80 bits). Anyone who knows one can call its mocks, so they must not be guessable, even though they only need to be unique within the org.

### 2.2 Org handle

* The handle is **Shadow Mock's own immutable identifier for the org**, not the live Clerk slug. It defaults to the Clerk slug when the org first enables Shadow Mock, and is stored in `orgs.handle`.
* Renaming the org in Clerk does **not** change the handle, so SUT configuration never breaks. If an org admin changes the handle in the management app, the old one is kept as a permanent alias (`org_handle_aliases`).
* Format: lowercase `a-z0-9-`, 3–40 characters, starting with a letter. Reserved: `__admin`, `__health`, `__ready`, `d`, `api`, `www`, `static`, `assets`, and anything starting with `__`.
* Management app URLs use the Clerk slug (for example `https://shadowmock.app.kaizentis.xyz/acme/mappings`). Its settings page shows the org's engine base URL, built from the handle.

## 3. Identity and access

### 3.1 Credentials

| Caller | Credential | Verified by the engine |
|---|---|---|
| Human, through the management app (runtime calls, §5.2) | Clerk session JWT from the `shadowmock` JWT template, forwarded by the Next.js server | Clerk JWKS signature; `iss`, `aud`, `exp`/`nbf`; `azp` in the allowed-origins list |
| CI pipelines and test code | Short-lived **Clerk M2M JWT**, obtained by exchanging a CI key (§3.6) | Clerk JWKS signature, same code path |
| SUT mock traffic | None | Org from the path handle; optional IP allowlist (§4) |

**The engine accepts JWTs only, and trusts only Clerk.** It has no API-key table and no opaque-token lookup.

### 3.2 JWT verification and claim mapping

Clerk's default session token has changed shape between versions: organisation claims are `org_id`, `org_role` and `org_slug` in v1, and nested under `o` in v2. To avoid depending on that, the management app requests a **Clerk JWT template** named `shadowmock`:

```json
{
  "aud": "shadowmock",
  "org_id": "{{org.id}}",
  "org_role": "{{org.role}}",
  "email": "{{user.primary_email_address}}"
}
```

Engine config:

```yaml
auth:
  driver: jwt
  issuers:
    - name: clerk-users
      jwksUrl: https://clerk.kaizentis.xyz/.well-known/jwks.json
      issuer:  https://clerk.kaizentis.xyz
      audience: shadowmock
      userClaim: sub
      orgClaim: org_id          # dotted paths allowed, e.g. "o.id"
      rolesClaim: org_role
      roleMap: { "org:admin": admin, "org:member": editor, "org:viewer": viewer }
    - name: clerk-ci            # Clerk M2M JWTs from the CI key exchange (§3.6)
      jwksUrl: https://clerk.kaizentis.xyz/.well-known/jwks.json   # confirm iss/aud/sub (§13)
      issuer:  https://clerk.kaizentis.xyz
      userClaim: ci_key_id
      userPrefix: "ci:"         # Principal.UserID = "ci:<ci_key_id>"
      orgClaim: org_id
      rolesClaim: org_role
      roleMap: { "org:member": editor }
  revocation:
    jtiDenylist: off            # optional; CI tokens live ≤ 60 min and keys are revocable
```

A token with no active organisation (a personal account) is rejected with `403 no_active_org`.

### 3.3 Admin vs mock traffic, and the org-match check

| Under `/<org-handle>/…` | JWT | Checks, in order |
|---|---|---|
| `__admin/…` | **Required** | 1. IP allowlist, if the org has `applyToAdmin` on. 2. Signature and claims. 3. Handle → `org_id`. 4. **JWT `org_id` = handle's `org_id`**, else `403 org_mismatch` ("token is for org X, URL is for org Y; switch active org"). 5. Role and global-write policy (§3.5) |
| Everything else: mock traffic, any HTTP method | **None** | IP allowlist (§4), quotas (§10.3) |

* The JWT is the source of truth for identity, and the path must agree with it. This stops a user in several Clerk orgs from silently writing to whichever org is active, and means a routing or lookup bug cannot cause cross-tenant access. Handle aliases resolve to the same `org_id`, so they pass.
* Mock traffic can change scenario state, but only within the layer it is attributed to. That is by design.
* The SUT's own `Authorization` header is never interpreted on mock traffic, because it belongs to the real dependency.

### 3.4 Teams (platform)

Teams are a **platform** concept owned by the platform shell and reused by every Kaizentis app. A team (for example "Claims squad") groups users and machine accounts within one Clerk org. Clerk orgs have no sub-groups, so teams cannot live in Clerk. **Authentication stays with Clerk. Teams are authorisation data, looked up when needed, and never put into tokens** (alternatives in Appendix A).

1. The user signs in through Clerk. The token carries `sub` and `org_id` only.
2. Org admins manage teams in the platform shell. The data lives in the platform schema (§6.1). A trigger publishes `pg_notify('platform_membership', {org_id, principal_id})` on every change. Apps get read-only access to the schema.
3. An app that needs team checks verifies the Clerk JWT, then looks up the principal's teams, cached as `mbr:{org}:{principal}` for 5 minutes and invalidated by the notification.

**In Shadow Mock, only the management app checks teams. The engine never does.**

### 3.5 Access matrix

| Action | Where | Who |
|---|---|---|
| Call mocks | Engine | Anyone, subject to the IP allowlist and quotas |
| Create, change and delete key-based and URL-based shadow mocks and their mappings; verify; read journals; scenario state; recordings | Engine admin API (CI, tests, scripts) or the management app | **Any member of the org** with the `editor` role: every Clerk `org:member` and `org:admin`, and CI tokens. An optional custom `org:viewer` role is read-only |
| Change the org's **global session** mappings, files and settings | **Management app only**, writing to the database | Org admins, plus teams granted `global:write` |
| Org settings: handle, IP allowlist, journal settings | Management app | Org admins |
| CI keys | Management app | Org admins, plus teams granted `ci-keys:manage` |
| Team grants inside Shadow Mock (`team_grants`) | Management app | Org admins |

In SaaS mode the engine's admin API rejects global-scope writes with `403 global_writes_via_management_app` (`admin.globalWrites: deny`). In standalone mode they are allowed, for WireMock parity.

### 3.6 CI credentials: CI key → short-lived Clerk M2M JWT

```
 CI job                         Management app (Vercel)                     Clerk                 Engine
   │  POST /api/ci/token             │                                        │                      │
   │  (client_id + client_secret) ──►│ 1. look up key, constant-time hash    │                      │
   │                                 │    compare, check revoked/expired/org  │                      │
   │                                 │ 2. cached token still valid? → return  │                      │
   │                                 │ 3. createToken({tokenFormat:'jwt',     │                      │
   │                                 │      claims:{org_id, org_role,         │                      │
   │                                 │      ci_key_id}, ttl}) ───────────────►│                      │
   │◄── {access_token, expires_in} ──│◄───────────────────────── signed JWT ──│                      │
   │                                                                                                 │
   │  /my-org/__admin/sessions/key-based   Authorization: Bearer <JWT>  ────────────────────────────►│
   │                                                    verify vs Clerk JWKS, org_id == my-org ◄─────┤
```

**CI keys** (management app; table `ci_keys`, §6.1)

* Each key belongs to one org and has a label (for example "claims-api GitHub Actions").
* `client_id` = `ck_<16 base32>` (public). `client_secret` = `cks_<52 base32>` (256 bits), **shown once**, stored only as a SHA-256 hash. That is sufficient because the secret is high-entropy random, not a password.
* Optional expiry (default none, with a reminder at 12 months), revoke, rotate (create the new key, then revoke the old one), and `last_used_at`/`last_used_ip` shown in the UI.

**Exchange endpoint:** `POST https://shadowmock.app.kaizentis.xyz/api/ci/token` (final host set by the platform)

* OAuth2 client-credentials shape: `grant_type=client_credentials`, credentials as HTTP Basic or form fields. Response `{"access_token":"…","token_type":"Bearer","expires_in":3600}`. Errors use OAuth2 codes and never reveal whether a key exists.
* Checks: hash matches (constant time), not revoked, not expired, org active, and the IP allowlist if `applyToAdmin` is on.
* Rate limits: 60/min per key and 300/min per source IP, with back-off on repeated `invalid_client`.
* Minting: Clerk `m2m.createToken` with `tokenFormat: 'jwt'`, `secondsUntilExpiration: 3600`, claims `{ org_id, org_role, ci_key_id }`. It uses one Clerk machine, `shadowmock-ci`, whose secret key lives only in the management app's server environment.
* **Token reuse:** while a key's last token has more than 10 minutes left, the endpoint returns it instead of minting a new one. This keeps mints at roughly one per key per hour (cost in §11.1). The cache is encrypted (Upstash or Vercel KV, AES-GCM) and can be added once mints approach Clerk's free 2,500 a month.
* Every exchange is audit-logged (`key_id`, IP, result).

**Engine side:** the `clerk-ci` issuer (§3.2) and the org-match check (§3.3), unchanged. Sessions and journals record `ci:<ci_key_id>`. There is no `jti` denylist: tokens live at most 60 minutes and revoking the key stops new ones.

**Using it in CI**

```bash
TOKEN=$(curl -s -u "$SM_CLIENT_ID:$SM_CLIENT_SECRET" \
  -d grant_type=client_credentials https://shadowmock.app.kaizentis.xyz/api/ci/token | jq -r .access_token)

curl -H "Authorization: Bearer $TOKEN" -XPOST \
  https://shadowmock.kaizentis.xyz/my-org/__admin/sessions/key-based -d '{"ttlSeconds":600}'
```

A `shadowmock` CLI or GitHub Action can wrap this later. Jobs longer than 50 minutes re-request a token and get the cached one back until it nears expiry.

## 4. Per-org IP allowlist

An org can restrict **calls to its mocks** to a set of source IP ranges. Org admins configure it in the management app, and it is stored in `org_ip_allowlist`.

* **Entries:** IPv4 or IPv6 CIDRs (`203.0.113.0/24`, `2001:db8::/48`, single addresses as `/32` or `/128`), each with a label. The maximum number of entries is set by the plan (default 100). The allowlist is available on Team and higher tiers (§12.3).
* **Scope:** all mock traffic for the org (global, key-based and URL-based), checked **before** the body is read, session detection or matching. `applyToAdmin` (default off) also applies it to the admin API, on top of the JWT.
* **Empty list = allow all.** The management app warns before saving a list that excludes the admin's own IP when `applyToAdmin` is on.
* **Rejection:** `403 {"error":"ip_not_allowed","ip":"<seen ip>"}`. Rejections are counted in stats (§10) but not journaled and not billed.
* **Client IP:** taken from the `X-Forwarded-For` entry added by Render's proxy, using a trusted hop count (§9.1). Client-supplied entries further left are ignored, so the IP cannot be spoofed. The source is configurable (`server.clientIp`), so another host only needs a config change.
* **Mechanics:** part of the org's in-memory settings snapshot (`[]netip.Prefix`), refreshed by change notifications. No database or Redis call per request.
* **Tell users:** cloud CI runners (for example GitHub-hosted) use large, changing IP ranges. Route test traffic through a fixed NAT or egress IP, or use self-hosted runners, before relying on the allowlist.

## 5. Management app vs engine

The management app and the engine share one database, so each owns clear areas.

### 5.1 The management app writes directly to the database

* Global session mappings and `__files`. This is **the only write path for the global session**, gated by team grants (§3.5). Files are uploaded by the browser straight to object storage through presigned URLs, with a metadata row in `files`.
* Longer-lived URL-based shadow mocks and their mappings created from the UI. URL-based mocks default to a 24 h TTL with a 30-day maximum, renewable (FR-7.2), and count as concurrent sessions while active (§10.2).
* Org settings: handle, IP allowlist, journal settings and detection options.
* CI keys, and the CI token exchange endpoint (§3.6).
* Reads: journal search, stats dashboards, usage and session lists.

All of its queries run as the signed-in user under Supabase row-level security keyed on the Clerk `org_id` (§9.3).

### 5.2 The management app calls the engine's API

These operations work on runtime state or need engine logic:

* Scenario state: view and reset (it lives in Redis).
* Near-miss analysis for a journal entry.
* "Test this mapping" dry-run (`POST /__admin/mappings/dry-run`) and validation before saving (`POST /__admin/mappings/validate`, the same code as the engine).
* Starting and stopping recordings.
* Live stats (`GET /__admin/stats/live`) and usage (`GET /__admin/usage`).

### 5.3 CI and tests call the engine's API only

They create and delete sessions, add session mappings and run verification through the WireMock-compatible, session-scoped API (DESIGN §10), using a CI JWT. The engine writes to the same tables, so the results appear in the management app. CI cannot change global mappings.

### 5.4 Shared-schema contract

* **One owner for migrations:** the Go repository (`migrations/`, applied by a release step). The management app generates its ORM types (Drizzle or Prisma) by introspection and never runs migrations.
* **One JSON Schema for mappings**, published from the Go repository as `schema/stub-mapping.schema.json`. Unknown fields are preserved (FR-1.4).
* **Change notification is done by database triggers**, not application code (§6.2), so neither writer can forget to notify the engine.

## 6. Data

### 6.1 Schema

```sql
-- ── Platform schema (owned by the platform shell; read-only for apps) ──────────────
create table platform.teams        (id text primary key, org_id text not null, name text not null, created_at timestamptz);
create table platform.team_members (team_id text references platform.teams(id) on delete cascade,
                                    principal_id text not null,     -- Clerk user id or machine account id
                                    role text not null,             -- owner | member
                                    primary key (team_id, principal_id));
create table platform.subscriptions (org_id text, app text, plan text, limits jsonb,
                                     period_start timestamptz, period_end timestamptz,
                                     primary key (org_id, app));    -- §12.5

-- ── Shadow Mock schema ─────────────────────────────────────────────────────────────
create table orgs (
  id         text primary key,                 -- Clerk org id (org_…); what JWTs carry
  handle     text unique not null,             -- immutable path handle (§2.2)
  plan       text not null,                    -- mirrored from platform.subscriptions
  limits     jsonb not null default '{}',      -- mirrored plan limits (§10.3)
  settings   jsonb not null default '{}',      -- journal.*, ipAllowlist.applyToAdmin, gzip, detection
  created_at timestamptz not null default now()
);

create table org_handle_aliases (handle text primary key, org_id text not null references orgs(id) on delete cascade);

create table org_ip_allowlist (
  org_id text not null references orgs(id) on delete cascade, cidr cidr not null, label text,
  created_by text not null, created_at timestamptz not null default now(),
  primary key (org_id, cidr)
);

create table team_grants (team_id text not null, org_id text not null, permission text not null,  -- global:write | ci-keys:manage
                          primary key (team_id, permission));

create table ci_keys (
  id text primary key, org_id text not null references orgs(id) on delete cascade, label text not null,
  secret_hash bytea not null, role text not null default 'org:member', created_by text not null,
  created_at timestamptz not null default now(), expires_at timestamptz, revoked_at timestamptz,
  last_used_at timestamptz, last_used_ip inet
);

create table sessions (
  id         text primary key,                 -- ses_… / URL-based mock id
  org_id     text not null references orgs(id),
  kind       text not null check (kind in ('key-based','url-based')),
  key_hash   text,                             -- session key, SHA-256 (raw key returned once)
  parent_id  text references sessions(id),
  name text, created_by text not null, expires_at timestamptz not null,
  masks      jsonb not null default '[]', metadata jsonb not null default '{}',
  created_at timestamptz not null default now()
);

create table session_tokens (                  -- FR-7.9 match tokens, unique per org
  org_id text not null, token text not null,
  session_id text not null references sessions(id) on delete cascade,
  primary key (org_id, token)
);

create table mappings (
  org_id     text not null references orgs(id),
  session_id text references sessions(id) on delete cascade,     -- null = global
  id uuid not null, priority int,
  body       jsonb not null,                   -- full WireMock StubMapping JSON
  insertion  bigserial, updated_at timestamptz not null default now(), updated_by text not null,
  scope_id   text generated always as (coalesce(session_id, '')) stored,
  primary key (org_id, scope_id, id)           -- same id may exist globally and in a session (FR-7.7)
);

create table files (org_id text, session_id text, name text, object_key text, size bigint,
                    content_type text, sha256 bytea, updated_at timestamptz, ...);

create table journal (                         -- partitioned by day; drop partitions for retention
  org_id text, session_id text, layer text, stub_id uuid, at timestamptz,
  method text, path text, status int, duration_ms int, matched boolean,
  req_meta  jsonb,                             -- headers, query, client ip, detect hit
  req_body  jsonb,                             -- {"inline": "..."} if < 4 KiB, else {"key","off","len"}
  resp_body jsonb
) partition by range (at);
create index on journal (org_id, session_id, at);
create index on journal (org_id, at);

create table stats_minute (org_id text, session_id text, minute timestamptz,
  requests bigint, matched bigint, unmatched bigint, p50_ms int, p99_ms int,
  primary key (org_id, session_id, minute));

create table usage_daily (                     -- meters (§10.2), one row per org per day
  org_id text, day date,
  mock_requests bigint, concurrent_sessions_samples int[],  -- 5-minute samples → monthly p95
  peak_concurrent_sessions int, session_minutes bigint, sessions_created bigint,
  journal_bytes bigint, ci_keys_active int, requests_rejected_ip bigint, requests_rate_limited bigint,
  primary key (org_id, day)
);
```

### 6.2 Change notification and cache coherence

A trigger on `orgs`, `org_handle_aliases`, `org_ip_allowlist`, `mappings`, `sessions`, `session_tokens` and `files` runs:

```sql
create function notify_change() returns trigger as $$
begin
  perform pg_notify('shadowmock_changes', json_build_object(
    'table', TG_TABLE_NAME, 'op', TG_OP,
    'org_id', coalesce(NEW.org_id, OLD.org_id),
    'session_id', coalesce(NEW.session_id, OLD.session_id),  -- adjusted per table
    'id', coalesce(NEW.id, OLD.id))::text);
  return null;
end $$ language plpgsql;
```

1. One engine instance (leader, by Redis lock lease) holds a persistent `LISTEN` connection (§9.3) and republishes each event to the Redis channel `sm:changes`.
2. Every instance subscribes and rebuilds the affected snapshot (layer, detector index, org directory or allowlist).
3. **Safety net:** each instance polls `max(updated_at)` per active org every 30 s and reloads any org whose version moved without an event.
4. Session creation through the engine API is **read-your-writes**: the creating instance applies it locally before responding. Others converge within the NFR-2.4 budget (100 ms target).

### 6.3 What lives where

| Data | Source of truth | Engine access |
|---|---|---|
| Orgs, handles, allowlists, mappings, sessions, tokens, masks, settings | Postgres | Compiled in-memory snapshots per org and layer, refreshed on change events. **Never read per request** |
| `__files` bodies | Object storage, metadata in `files` | Read-through LRU cache per instance |
| Scenario state | Redis (`scn:{org}:{scope}`) | Read and transition per request. Forked at session creation (DESIGN §6.3) |
| Session registry | Postgres (durable) + Redis (hot, with TTL) | A reaper deletes expired sessions from Postgres |
| Journal (hot and warm) | Redis; Postgres + object storage | §7 |
| Live counters | In memory per instance → Redis every second | Rolled up into `stats_minute` and `usage_daily` |
| Rate limits | Redis token buckets per org | Tokens fetched in small batches per instance |

### 6.4 Lazy tenant loading

Instances do not load every org at boot. An org's snapshots load on its first request (single-flight), are kept while active, and are evicted after about 30 minutes idle. Memory stays bounded as the number of orgs grows.

## 7. Journal

### 7.1 Tiers

| Tier | Store | Holds | Used for |
|---|---|---|---|
| **Hot** | Redis: a capped list per session, plus one per org for recent global traffic | Full entries. Bodies over 64 KiB are truncated and flagged | Verification, near misses and the session journal API during tests. Shared, so every instance sees a request as soon as it is served |
| **Warm** | Postgres row per request + large bodies in object storage | Lean, searchable rows; bodies inline if under 4 KiB | History, search and stats in the management app |

Hot caps apply per session and per org, and are set by plan (§12.3): Free 200, Starter and Team 1,000, Business 5,000. Session lists expire with the session, and org lists after 1 hour idle (decision B, §11.4).

### 7.2 Write path

1. Each served request is appended to its hot list (one pipelined `LPUSH`/`LTRIM`/`EXPIRE`) and to the instance's in-memory journal queue.
2. **Rows** flush every 250 ms or 500 entries, as one multi-row `INSERT`.
3. **Large bodies** (4 KiB or more) are appended to the instance's current body object, which is written **every 5 s or at 8 MB**. It is **one object per instance per flush, shared by all orgs** (decision A, §11.4). Each body is its own zstd frame, and its row records `{key, off, len}`. Rows pointing into an object are inserted only after it is written, so a crash can leave an orphan object (removed by lifecycle rules) but never a dangling row. History for large bodies lags by up to about 5 s, and the hot tier covers live needs.
4. The queue is bounded. On overflow it drops entries and counts them, and it never blocks mock traffic.

### 7.3 Read path (management app)

* List and search use Postgres only (the indexed summary columns).
* Opening an entry returns the inline body, or fetches `Range: bytes=off..off+len` server-side and decompresses that frame.
* Exports and large downloads use presigned GET URLs valid for 60 s.

### 7.4 Retention

* Object keys carry the plan's retention bucket (`r1`, `r7`, `r30`, `r90`). Bucket lifecycle rules delete by prefix and age, so no cleanup job is needed.
* Postgres drops daily partitions older than the longest retention, and a nightly job deletes rows of shorter-retention orgs.
* After a plan change, new writes use the new prefix and old data ages out under the old rule.

### 7.5 Per-org settings (org admins)

* `journal.capture`: `all` (default) | `sessions-and-unmatched` | `off`.
* `journal.bodies`: `on` (default) | `off`. With `off`, only metadata rows are kept, for orgs that must not store test payloads.
* Plan row caps per day. Beyond the cap, requests are still counted but no rows are written, and the management app shows a banner.

## 8. Object storage

**Metadata in Postgres, bulky bytes in object storage.** Both the engine and the management app use the S3 API, so the provider is a config setting. Cloudflare R2 is chosen because it has no egress fees, and bodies are downloaded whenever an entry is viewed. Supabase Storage or AWS S3 work unchanged.

| Data | Key layout | Written by | Read by |
|---|---|---|---|
| Journal bodies | `journal/r<retention>/<yyyy>/<mm>/<dd>/<hh>/<instance>-<seq>.zst` (shared, §7.2) | Engine | Management app; engine for near misses on history |
| `__files` bodies | `files/<org_id>/<scope>/<sha256>` (content-addressed; `files` maps name → key) | Management app (presigned upload); engine for session files and recordings | Engine (cached) |
| Recordings and exports | `exports/<org_id>/<id>.<ext>` | Engine | Management app (presigned download) |

**Isolation and security**

* The bucket is private. The only URLs are short-lived presigned ones.
* Shared journal objects carry no org in the key. They are reachable only through a journal row's `{key, off, len}`, and rows are org-filtered by the engine and by row-level security in the management app. Nothing lists objects.
* **Trade-off:** one org's bodies cannot be deleted on their own. When an org leaves, its rows are deleted immediately, which makes its bodies unreachable, and the bytes expire with retention. State this in the data-processing terms.
* `files/` and `exports/` keys always include the org, built from the authenticated org and never from user input.
* There are separate credentials for the engine (read and write on `journal/`, `files/`, `exports/`) and the management app (read on all, write on `files/` via presigned uploads only).
* Encryption at rest is the provider default. Customers are told not to send real personal data, and `journal.bodies: off` exists for those who can't guarantee that.

## 9. Hosting

| Component | Service | Plan to start |
|---|---|---|
| Engine | Render web service (Docker) | Pro workspace, 2 × Standard instances |
| Shared cache | Render Key Value | Starter (256 MB), sized from hot journal caps |
| Database | Supabase | **Pro from production launch** (Free for dev; free projects pause after a week idle) |
| Object storage | Cloudflare R2 | Pay as you go (10 GB free) |
| Shell and management app | Vercel | Pro (Hobby does not allow commercial use) |

**Region:** put the Render services and the Supabase project in the closest available pair of regions (for example Render Oregon with a US-West Supabase region, or both in Frankfurt). Measure the round-trip time before launch. Costs are in §11.

### 9.1 Engine on Render

* **Service:** a Docker image, configured entirely by environment variables in a Render environment group. Secrets (Supabase connection strings, Key Value URL, R2 keys) are secret env vars. The engine holds no Clerk secrets, only the public JWKS URL.
* **Domain and TLS:** `shadowmock.kaizentis.xyz` points to the service by CNAME, with Render-managed TLS.
* **Scaling:** autoscaling on CPU (target ~60 %), minimum 2 instances. Instances are stateless, so there is no stickiness.
* **Health:** Render's health check uses `/__ready` (Key Value and Postgres connected), which gates zero-downtime deploys. `/__health` is liveness only.
* **Client IP:** `server.clientIp: {header: X-Forwarded-For, trustedHops: 1}` (§4).
* **Shutdown:** on SIGTERM, stop accepting, drain in-flight requests (including delays, up to a cap), flush the journal queue, and exit within Render's grace period.
* **Request duration:** cap `fixedDelayMilliseconds` below Render's maximum request duration.
* **Faults:** Render's proxy terminates client connections, so connection-level faults (FR-3.3) reach clients as proxy errors (typically 502), not raw resets. Document this. If true TCP faults become a requirement, the same image can run on Fly.io with a raw TCP service.
* **Egress:** proxy and webhook targets pass an SSRF allow/deny policy (block private, link-local and metadata ranges).

### 9.2 Render Key Value

* Connected over Render's **private network**, and not exposed to the internet. The management app never connects to it; it uses the engine API for live data (§5.2).
* Hot-path Redis use is kept small. Per request it handles only scenario state (for stubs that use scenarios), the hot journal append and batched rate-limit tokens. Stats counters are added up in memory and flushed every second.
* Eviction policy is `noeviction`, so scenario and session state are never dropped silently. Memory is sized from hot journal caps.

### 9.3 Supabase Postgres

| Client | Connection | Why |
|---|---|---|
| Engine `LISTEN` leader (§6.2) | **Session pooler** (IPv4, real session) or direct connection | `LISTEN` needs a persistent session; the transaction pooler can't hold one |
| Engine: snapshots, journal batches, session writes | Session pooler; `pgxpool`, max ~10 per instance | Long-lived processes |
| Management app on Vercel | **Transaction pooler** | Many short-lived serverless connections |

* **Row-level security:** Clerk is configured as a third-party auth provider in Supabase. The management app queries as the signed-in user, and RLS restricts every Shadow Mock table to rows matching the Clerk JWT's `org_id`. The engine uses a service role and filters by org in code.
* **Not used:** Supabase Auth (Clerk) and Supabase Realtime (`LISTEN` → Redis is simpler).
* **Backups:** Pro's daily backups at launch; add point-in-time recovery once paying customers depend on it.
* **Migrations:** run from the Go repository by a release step (§5.4), over the direct or session connection.

## 10. Stats and usage metering

### 10.1 Stats (management app dashboards)

Read from `stats_minute`, or live via `GET /__admin/stats/live`:

* Requests per minute by org, session and layer (key-based, URL-based or global).
* Match rate, unmatched count and top unmatched paths.
* Latency p50 and p99 (engine time, excluding configured delays).
* Detection stage hits (header, query, cookie, metadata scan, body scan) and ambiguity events.
* Active sessions and expiries.
* IP allowlist rejections by source IP (top 20), so admins can spot a missing range.

### 10.2 Meters (`usage_daily`, reported daily to platform billing, §12.5)

| Meter | Aggregation | Billed? |
|---|---|---|
| `mock_requests` | Sum. Excludes admin calls, IP rejections and rate-limited requests | **Yes**: included per tier, then overage |
| `concurrent_sessions_p95` | 95th percentile of 5-minute samples of active sessions (key-based and URL-based) over the month | **Yes**: included per tier, then overage |
| `peak_concurrent_sessions` | Max per day | Visibility; hard-ceiling check |
| `session_minutes`, `sessions_created` | Sum | Visibility only. Billing on sessions created would discourage a session per test |
| `journal_bytes` | Sum | Visibility. Retention is sold by tier and add-on |
| `ci_keys_active` | Max | Tier limit |
| `requests_rejected_ip`, `requests_rate_limited` | Sum | Visibility. **Not** billed |

### 10.3 Enforcement

Plan limits come from `orgs.plan` and `orgs.limits` (mirrored from platform entitlements, §12.5), cached per instance and enforced with Redis token buckets. The limits are requests per second, included sessions and the hard ceiling, hot journal size, retention, CI keys and the IP allowlist entitlement. Hard-limited mock traffic returns `429` with a JSON reason, so a failing test shows why.

## 11. Cost model

> Approximate published prices, October 2026. Re-check before budgeting. The purpose is to show **what drives cost**, not exact figures.

### 11.1 Cost drivers

| # | Driver | Usage that drives it | Unit cost (approx.) | Control | Level |
|---|---|---|---|---|---|
| 1 | **Clerk organisations** | Number of customer orgs | Clerk Pro ~$25/mo. B2B add-on ~$100/mo for 100 orgs, then **~$1 per org per month** | Archive inactive free orgs | **Platform**, once per org (decision D) |
| 2 | **Clerk M2M mints** | Active CI keys × active hours | $0.001 per mint after 2,500 a month | Token reuse (§3.6). Longer token lifetime trades off revocation speed | App |
| 3 | **Render bandwidth** | Mock requests × response size | Pro includes 25 GB, then **$0.15/GB** | **Gzip by default** (decision C), typically 3–5× smaller for JSON | App |
| 4 | **Render compute** | Peak requests per second, body scanning, templating, open connections from long delays, active orgs in memory | ~$25/mo per 2 GB instance, minimum 2 | Plan rate limits; lazy tenant loading (§6.4) | App |
| 5 | **Key Value memory** | Hot journal: concurrent sessions × entries × size, plus per-org lists | ~$10 (256 MB) to ~$100 (5 GB) a month | Caps and truncation (decision B) | App |
| 6 | **Supabase** | Journal rows: requests × ~400 B × retention days. Insert rate sets compute size | Pro $25 including 8 GB, then $0.125/GB; compute upgrades extra | Retention, `journal.capture`, row caps | App |
| 7 | **R2** | Large-body bytes × retention; number of object writes | $0.015/GB-mo; ~$4.50 per million writes | Shared per-instance objects (decision A) | App |
| 8 | **Vercel** | Seats and function usage | ~$20 per seat per month | – | Platform + app |
| 9 | **Observability** | Log volume and distinct metric series | Free to ~$50+ | Metric labels by org only, never by session, path or IP | App |
| 10 | **Payment processing** | Revenue | Stripe ~2.9 % + $0.30 per charge | Annual billing | Platform |
| 11 | **Fixed base fees** | None | Render workspace, Supabase Pro, Clerk Pro, Vercel | – | Platform + app |

### 11.2 Sample usage and monthly cost

Assumptions: ~3.5 KB response per request, 30-day retention, CI keys active ~260 h a month.

| | Starter | Growth | Scale |
|---|---|---|---|
| Orgs / requests a month / peak concurrent sessions | 10 / 2M / 20 | 100 / 30M / 300 | 1,000 / 300M / 3,000 |
| Management users / active CI keys | 30 / 20 | 500 / 200 | 5,000 / 2,000 |
| Render compute | ~$50 + workspace | ~$75 + workspace | ~$350–500 |
| Render bandwidth | $0 (7 GB) | ~$12 (105 GB) | ~$150 (~1 TB); ~$40–50 with gzip |
| Key Value | ~$10–25 | ~$60–100 | ~$100+ |
| Supabase | $25 | ~$40 | ~$150–250 (time to move the journal to ClickHouse) |
| R2 | ~$0 | ~$5 | ~$10–15 |
| Clerk, platform (orgs) | $25 (+$100 for the B2B add-on when needed) | ~$125 | ~$1,025 |
| Clerk, app (M2M) | ~$3 | ~$60 | ~$130–620 |
| Vercel and observability | ~$20–40 | ~$40–110 | ~$150–400 |
| **Rough total per month** | **~$150–300** | **~$500–650** | **~$2,000–3,500** |

### 11.3 Unit economics

| Unit | Approximate cost | Mostly |
|---|---|---|
| 1 million mock requests | ~$1–2 (under $1 with gzip) | Bandwidth, then journal storage |
| 1 org per month | ~$1 (platform) | Clerk organisation fee |
| 1 active CI key per month | ~$0.26 | Clerk M2M mints |
| 1 concurrent session | A few MB of Redis | Hot journal |
| 1 GB of journal history | ~$0.125 (Postgres) / ~$0.015 (R2) | Retention |

### 11.4 Cost-driven design decisions

* **A. Shared journal body objects:** one object per instance per flush (5 s or 8 MB), not one per org per batch. Per-org objects would mean hundreds of millions of writes a month at 1,000 orgs (~$1,000+). Shared objects cap writes at about 520k per instance per month. Isolation comes from org-filtered rows. Per-org body deletion happens by expiry only (§8).
* **B. Bounded hot journal:** entry caps per session and per org, set by plan (200–5,000, default 1,000), with bodies over 64 KiB truncated in the hot copy (§12.3). Without caps, the Scale tier could need tens of GB of Redis.
* **C. Gzip by default** when the client sends `Accept-Encoding: gzip`, as WireMock does. A mapping can opt out (`"gzipDisabled": true`), and so can an org setting. This is the biggest lever on the biggest variable cost.
* **D. Clerk's per-org fee is a platform cost**, charged once per org through the platform org fee (§12.2). Apps never carry it.

### 11.5 Year one: free customers only, first paying customer at month 12

**Assumptions**

* Production launches in month 1 on the stack in §9, with Supabase Pro from launch.
* Free orgs grow from 5 in month 1 to ~30 new a month by month 12, reaching **185 cumulative**. About 60 % stay active (inactive free orgs are archived after 90 days), so **~110 are active** by month 12.
* An average active free org uses **40k requests a month** (hard cap 100k), 1 concurrent session and 1 CI key for ~40 hours a month. Free retention is 1 day. Responses average ~3.5 KB before gzip.
* Clerk Pro, with retained orgs above 100 at ~$1 each. The B2B add-on is not needed while free orgs are capped at 5 members (to verify, §13).

**Fixed monthly base (~$164)**

| Item | $/month |
|---|---|
| Render Pro workspace | 25 |
| Engine: 2 × Standard (HA minimum) | 50 |
| Dev/staging engine (Starter) + free dev Key Value | 7 |
| Render Key Value Starter (256 MB) | 10 |
| Supabase Pro (production) + free dev project | 25 |
| Clerk Pro | 25 |
| Vercel Pro, 1 seat | 20 |
| Domain, transactional email (free tier), misc. | 2 |
| R2 (inside 10 GB free), observability (free tier), Stripe (no revenue) | 0 |

**Month by month**

| Month | Cumulative free orgs | Active | Mock requests | Bandwidth | Variable costs | **Total** |
|---|---|---|---|---|---|---|
| 1 | 5 | 5 | 0.2M | <1 GB | $0 | **$164** |
| 2 | 12 | 10 | 0.4M | 1 GB | $0 | **$164** |
| 3 | 20 | 15 | 0.6M | 2 GB | $0 | **$164** |
| 4 | 30 | 20 | 0.8M | 3 GB | $0 | **$164** |
| 5 | 42 | 26 | 1.0M | 4 GB | $0 | **$164** |
| 6 | 55 | 33 | 1.3M | 5 GB | $0 | **$164** |
| 7 | 70 | 42 | 1.7M | 6 GB | $0 | **$164** |
| 8 | 88 | 53 | 2.1M | 7 GB | ~$1 (M2M) | **$165** |
| 9 | 108 | 65 | 2.6M | 9 GB | ~$1 | **$165** |
| 10 | 130 | 78 | 3.1M | 11 GB | ~$2 | **$166** |
| 11 | 155 | 93 | 3.7M | 13 GB | ~$2 | **$166** |
| 12 | 185 | 111 | 4.4M | 15 GB | ~$3 M2M + ~$11 Clerk orgs over 100 | **$178** |
| **Year** | | | **~22M** | | | **≈ $2,000** |

**What this shows**

* **Year one is almost all fixed cost.** Bandwidth stays inside Render's included 25 GB. Redis fits 256 MB (~110 orgs × ~1.2 MB: 200 entries per session plus 200 per org at ~3 KB each). One-day retention keeps Postgres and R2 negligible.
* **Marginal cost of a free org:** ~$0.05–0.10 a month in infrastructure, plus ~$1 once retained orgs pass 100.
* **Worst case with hard caps:** all 185 orgs at the 100k cap is 18.5M requests and ~65 GB of bandwidth, about $6 of overage. The free tier cannot run away.
* **Leaner start (optional):** a private beta in months 1–3 on Render Hobby (one instance), Supabase Free, Clerk free and Vercel Pro costs about **$55–60 a month**, bringing the year to **~$1,700**. The trade-off is no high availability, and the Supabase project pauses if idle for a week.
* **Break-even** at ~$165–180 a month: about **4 orgs on Team platform + Starter app ($48 each)**, or 2 on Team platform + Team app ($118 each).
* **Not included:** your time, legal and incorporation, marketing, support for paid tiers, and vendor price changes.

## 12. Pricing and billing (proposal)

### 12.1 Structure

`monthly bill = platform org fee + Σ over the org's apps (app tier base + app overages)`

* The **platform org fee** covers costs that exist once per org, whatever apps it uses: Clerk organisation and SSO fees, the platform shell, billing and support.
* Each **app** prices its own tiers on its own cost drivers and meters. Shadow Mock's are requests, concurrent sessions and retention. A future app brings different meters without touching the platform fee.

### 12.2 Platform org fee (Kaizentis)

| | **Free** | **Team** | **Business** | **Enterprise** |
|---|---|---|---|---|
| Price per org per month | $0 | **$19** | **$79** | Custom |
| Members | Up to 5 | Unlimited | Unlimited | Unlimited |
| Teams (§3.4) | – | ✓ | ✓ | ✓ |
| SSO (SAML/OIDC through Clerk) | – | – | ✓ | ✓ |
| Audit log | – | 30 days | 1 year | Custom |
| Support | Community | Email | Priority email | SLA |
| App tiers allowed | Free tiers only | Any | Any | Any, including custom |

Free orgs inactive for 90 days are archived, because Clerk charges per retained org. Business absorbs Clerk's SSO connection cost (to verify, §13).

### 12.3 Shadow Mock app tiers

| | **Free** | **Starter** | **Team** | **Business** | **Enterprise** |
|---|---|---|---|---|---|
| Price per org per month | $0 | **$29** | **$99** | **$399** | Custom |
| Mock requests included per month | 100k (**hard cap**) | 2M | 15M | 50M | Custom |
| Concurrent sessions included (p95) | 2 | 10 | 50 | 250 | Custom |
| Rate limit (requests/s per org) | 10 | 50 | 200 | 1,000 | Custom |
| Journal retention | 1 day | 7 days | 30 days | 90 days | Custom |
| Hot journal entries (per session and per org) | 200 | 1,000 | 1,000 | 5,000 | Custom |
| Session TTL maximum (key-based / URL-based) | 24 h / 7 days | 24 h / 30 days | 24 h / 30 days | 24 h / 30 days | Custom |
| Active CI keys | 1 | 5 | 25 | 100 | Custom |
| Per-org IP allowlist | – | – | ✓ | ✓ | ✓ |
| Journal capture and body controls | – | ✓ | ✓ | ✓ | ✓ |
| **Overage per extra 1M requests** | – (429 at cap) | $3 | $2 | $1.50 | Negotiated |
| **Overage per extra concurrent session** | – (429 at cap) | $3 | $2 | $1.50 | Negotiated |
| Add-on: +30 days retention | – | – | $29 | $49 | – |

* Annual billing gets 2 months free (~17 %).
* Meters are defined in §10.2. Session caps are soft on paid tiers: sessions beyond the included amount succeed and count as overage, up to a hard ceiling of 3× the included amount to stop runaway CI. Free tiers are hard-capped.

### 12.4 Margins and example bills

| Tier at full included usage | Price | Est. cost to serve | Gross margin |
|---|---|---|---|
| Starter (2M requests, 10 sessions, 7 days) | $29 | ~$5 | ~80 % |
| Team (15M requests, 50 sessions, 30 days) | $99 | ~$25–30 | ~70 % |
| Business (50M requests, 250 sessions, 90 days) | $399 | ~$150–180 | ~55 % |
| Overage, 1M requests | $1.50–3 | ~$0.50–1 | 50–80 % |

* Most orgs use well below their included amounts, so real margins are higher. Watch Business: its 90-day retention and 5,000-entry hot journals are the cost drivers.
* Break-even on the fixed base is in §11.5.

| Example org | Platform | Shadow Mock | Total per month |
|---|---|---|---|
| Small team trying it out | Free $0 | Free $0 | **$0** |
| One squad running CI | Team $19 | Starter $29 | **$48** |
| Mid-size org, shared test environment, 20M requests, 60 p95 sessions | Team $19 | Team $99 + 5M × $2 + 10 × $2 = $129 | **$148** |
| Larger org with SSO, 60M requests, 300 p95 sessions | Business $79 | Business $399 + 10M × $1.50 + 50 × $1.50 = $489 | **$568** |

### 12.5 How billing works on the platform

* **Entitlements:** `platform.subscriptions` holds each org's plan per app. A change event mirrors Shadow Mock's row into `orgs.plan` and `orgs.limits`. The engine enforces from that copy (§10.3), and the management app shows usage against limits.
* **Meter manifest:** each app publishes its meters (name, unit, aggregation `sum` | `p95` | `max`, included amount per tier, overage price). The platform billing service needs no app-specific code.
* **Usage reporting:** each app reports `usage_daily` to the billing provider once a day per org per meter, idempotently keyed by `org/app/meter/day`, with a true-up at period end.
* **Provider:** Stripe Billing (subscriptions plus usage meters) is the safe default. Clerk Billing could run subscriptions and expose plan claims, but whether it supports usage-based overage needs checking (§13).
* **Caps:** soft caps never break a running test. The org is billed overage, and the management app warns at 80 % and 100 %. Hard caps (Free tiers, and 3× the session ceiling) return `429` with a JSON reason.

## 13. Verify before launch

| # | Item | Why it matters |
|---|---|---|
| 1 | Which header and position Render fills with the true client IP | The IP allowlist depends on it (§4, §9.1) |
| 2 | Whether Render can reach Supabase over IPv6; otherwise use the session pooler or the IPv4 add-on | Engine database connectivity (§9.3) |
| 3 | Render's maximum request duration | Cap on configurable mock delays (§9.1) |
| 4 | The exact `iss`, `aud` and `sub` of Clerk M2M JWTs, from a dev-instance token | `clerk-ci` issuer config (§3.2) |
| 5 | How Clerk charges orgs without the B2B add-on, and its per-SSO-connection price | Platform org fee levels (§11.5, §12.2) |
| 6 | Whether Clerk Billing supports usage-based overage | Billing provider choice (§12.5) |
| 7 | Render workspace pricing after its April 2026 plan change | Fixed base cost (§11.5) |
| 8 | All prices in §11–§12 | Approximate, October 2026 |

## 14. Engine impact summary

What the SaaS setup means for the Go engine (detail in DESIGN.md):

* **Storage providers:** `postgres` for configuration and journal rows, `redis` for scenario state, session registry, hot journal and change fan-out, and `s3` for `BlobStore`.
* **Auth:** JWT only, with a list of trusted Clerk issuers. No `jti` denylist by default.
* **Tenancy:** the org always comes from `/<org-handle>` via an in-memory org directory with aliases, and admin calls must have a matching JWT org.
* **Request path:** the per-org IP allowlist runs before the body is read, and detection uses the org's own index.
* **Journal:** a Redis hot tier for verification, plus a warm tier of batched rows and shared body objects.
* **Response compression:** gzip by default.
* **Metering:** meters go to `usage_daily`, and limits come from `orgs.limits`.
* **New admin endpoints** under `/<org-handle>`: `mappings/validate`, `mappings/dry-run`, `stats/live`, `usage`.
* **Configurable client-IP source**, for portability between hosts.

## Appendix A. Alternatives considered

| Topic | Chosen | Alternatives and why not |
|---|---|---|
| **Teams in tokens** | Clerk token + membership lookup, cached and invalidated on change | *Re-minting a platform token with teams:* needs a token service and signing keys, and is stale until expiry. *Teams in Clerk metadata claims:* claims are limited to ~1.2 KB, so it breaks with many teams and ties the permission model to Clerk |
| **CI credentials** | CI key → short-lived Clerk M2M JWT (option C) | *A: long-lived Clerk M2M JWT:* simplest, but slow to revoke without a denylist. *B: own OAuth2 token service:* immediate revocation and no lock-in, but you own signing keys, JWKS and rotation (moving to it later only changes the exchange endpoint). *Clerk opaque tokens:* not JWTs and need a Clerk call per verification. *"CI user" accounts:* 60-second sessions, an instance-wide secret in CI and a seat cost |
| **Mock-traffic addressing** | One host, org handle in the path | *Sub-domain per org or session:* needs wildcard DNS and TLS. *JWT on mock traffic:* every SUT would need a long-lived token header. *Random org entry ID:* works, but is unreadable |
| **Engine hosting** | Render | *AWS:* too complex for the team size. *Fly.io:* cheaper compute and true TCP faults, with a few more concepts; it is the fallback. *DigitalOcean App Platform:* autoscaling only on dedicated CPU. *Railway:* no horizontal autoscaling. *Cloud Run:* built for scale-to-zero, which conflicts with the persistent `LISTEN` and Redis connections |
| **Database** | Supabase | *Render Postgres:* slightly cheaper and on the same network, but no pooler for Vercel and no Clerk row-level security integration. *Neon:* the always-on `LISTEN` connection cancels scale-to-zero savings. *Prisma Postgres:* billed per operation, and `LISTEN` support is unconfirmed. *Self-hosted:* ops burden |
| **Customer journal store** | Postgres rows + R2 bodies, with a Redis hot tier | *Datadog or other APM:* about $1.70 per million events kept searchable, and not built for exposing data to customers. *Object storage only:* no search. *Data lake (Iceberg/Parquet):* deferred until long retention or very large volume is needed. *ClickHouse or Tinybird:* the step up past ~50–100M requests a month |
| **Object storage** | Cloudflare R2 | *AWS S3:* egress ~$0.09/GB. *Backblaze B2:* cheaper storage, with egress limits. *Supabase Storage:* fine as a single-vendor option |
