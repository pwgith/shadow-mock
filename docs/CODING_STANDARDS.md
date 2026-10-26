# Shadow Mock: Go Coding Standard

> Status: v1.0, 2026-10-26. Applies to all Go code in the engine repository.
> It is enforced by [`.golangci.yml`](../.golangci.yml) and [`scripts/lint.sh`](../scripts/lint.sh), and run by `make lint` and CI.

## 1. Sources and precedence

This standard adopts the Go community's established guidance and adds only what Shadow Mock's design needs:

1. [Effective Go](https://go.dev/doc/effective_go)
2. [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)
3. [Google Go Style Guide](https://google.github.io/styleguide/go/): guide, decisions and best practices
4. [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md), where it does not conflict with the above
5. **This document**, for project-specific rules (marked **[SM]**)

When sources disagree, the higher number loses, except that **[SM]** rules always apply. If a rule here is unclear, follow the Google guide.

**Toolchain:** Go 1.26 (`go 1.26` in `go.mod`), golangci-lint v2 and govulncheck. Versions are pinned in the `Makefile` and CI.

## 2. How each rule is enforced

| Area | Rule | Enforced by |
|---|---|---|
| Formatting | gofumpt formatting; imports grouped std / third-party / local | `formatters: gofumpt, goimports` |
| Naming | MixedCaps, initialisms, receiver names, no stutter, error naming | `revive`, `staticcheck` (ST1003, ST1016), `errname`, `predeclared`, `scripts/lint.sh` (file names) |
| Docs | Doc comment on every exported identifier and package; comments are sentences | `revive` (exported, package-comments), `godot` |
| Errors | Always checked, wrapped with `%w`, compared with `errors.Is/As` | `errcheck`, `errorlint`, `nilerr`, `nilnil`, `revive` (error-strings) |
| Context | `ctx` first; never stored in structs; outbound HTTP has a context | `revive` (context-as-argument), `containedctx`, `noctx`, `contextcheck` |
| Concurrency | No copied locks; loop variables; goroutine hygiene | `govet` (copylocks, loopclosure), `copyloopvar`, tests with `-race` |
| Architecture **[SM]** | Hot path never imports storage drivers; slog only; no banned libraries | `depguard` |
| Time and randomness **[SM]** | `internal/clock` only; `crypto/rand` for identifiers | `forbidigo`, `depguard`, `gosec` (G404) |
| HTTP **[SM]** | No default client or package-level `http.Get`; response bodies closed | `forbidigo`, `bodyclose`, `noctx` |
| Database | Parameterised SQL; rows closed and checked | `gosec` (G201/G202), `sqlclosecheck`, `rowserrcheck` |
| Logging **[SM]** | slog, key-value pairs, snake_case keys, context-aware | `sloglint`, `forbidigo` (`fmt.Print*`) |
| JSON **[SM]** | WireMock wire model uses camelCase tags; marshalled structs are tagged | `tagliatelle`, `musttag` |
| Complexity | Cognitive complexity ≤ 20, `if`-nesting complexity ≤ 5, functions ≤ 80 lines / 50 statements | `gocognit`, `nestif`, `funlen` |
| Packages and TODOs | No `util`/`common`-style package names; TODOs name an owner or issue | `scripts/lint.sh` |
| Testing | `t.Helper`, `t.Parallel` used correctly; no assertion libraries | `thelper`, `tparallel`, `depguard` |
| Security | gosec rules; known-vulnerable dependencies | `gosec`, `govulncheck` |
| Suppression | `//nolint` must name the linter and give a reason | `nolintlint` |
| Modules | `go.mod`/`go.sum` tidy | `scripts/lint.sh` (`go mod tidy -diff`) |

Rules without an automated check are marked **(review)** and enforced in code review.

## 3. Formatting and layout

* Code is formatted with **gofumpt** (a stricter superset of gofmt), with imports managed by **goimports** in three groups: standard library, third-party, then `github.com/pwgith/shadow-mock/...`. `make fmt` applies both.
* There is no hard line-length limit (Google style), but prefer breaking long signatures and calls at about 120 columns. Do not break lines just to fit a limit. **(review)**
* One package per directory. File names are `lower_snake_case.go`; tests are `*_test.go` and platform files `*_linux.go`. `scripts/lint.sh` rejects other file names.
* Order within a file: package doc, imports, constants, variables, types (each followed by its constructor and methods), then functions. **(review)**

## 4. Naming

* **Packages:** short, lower case, a single word, no underscores or plurals, and not `util`, `utils`, `common`, `helpers`, `misc` or `base` (`revive` var-naming, plus the package-name check in `scripts/lint.sh`). Name for what the package provides: `detect`, `counter`, `blob`.
* **No stutter:** `session.Session` is fine, but `session.SessionStore` is not; use `session.Store`.
* **MixedCaps:** never underscores in Go identifiers, except in tests (`TestResolve_layerBeatsPriority`) and generated code.
* **Initialisms** keep a consistent case: `ID`, `URL`, `HTTP`, `JSON`, `XML`, `JWT`, `JWKS`, `IP`, `TTL`, `API`, `SQL`, `UUID`, `TLS`, `CIDR`, `SSRF`. For example `urlMockID`, `ParseJWT`, `clientIP`, `HTTPClient`. Enforced by `staticcheck` ST1003 with the project initialisms list.
* **Receivers:** one or two letters, consistent across a type's methods, never `this` or `self`.
* **Getters** have no `Get` prefix (`s.Key()`, not `s.GetKey()`). Setters are `SetKey`.
* **Interfaces:** single-method interfaces are named method + `er` (`Detector`, `Authenticator`). Define interfaces where they are **used**, not where they are implemented (Go Code Review Comments). The storage interfaces in `internal/store` are the deliberate exception, because they are the plug-in boundary.
* **Errors:** sentinel variables `ErrXxx`, and types `XxxError` (`errname`).
* **Constants:** MixedCaps, never ALL_CAPS. Group related constants with typed iota, or string constants for wire values: `session.KeyBased Kind = "key-based"`.
* **Domain terms [SM]:** use the glossary in REQUIREMENTS.md §2 exactly: *global session*, *key-based shadow mock* (`KeyBased`), *URL-based shadow mock* (`URLBased`), *session*, *session key*, *match token*, *layer*, *org handle*. Never use the old terms "shadow session", "dedicated" or "tenant key". **(review)**

## 5. Packages and architecture [SM]

The layout follows DESIGN.md §3. Dependency rules are enforced by `depguard`:

| Rule | Why |
|---|---|
| `internal/engine`, `internal/match`, `internal/detect` and `internal/respond` must not import `internal/store/postgres`, `internal/store/redis`, `database/sql`, `pgx` or `go-redis` | The request hot path works only from in-memory snapshots (NFR-3.3) |
| Nothing outside `internal/store/...` imports `pgx` or `go-redis` | Storage drivers stay behind the repository interfaces (NFR-3.2) |
| `pkg/extension` imports only the standard library and `internal`-free packages | It is the only public API |
| No package imports `cmd/...` | Wiring lives only in `cmd/shadowmock` |
| `log`, `logrus`, `zap` and `zerolog` are banned; use `log/slog` | One logging system |
| `github.com/pkg/errors` and `io/ioutil` are banned | Use the standard library `errors` and `io`/`os` |
| `math/rand` and `math/rand/v2` are banned everywhere except `internal/respond` (delay distributions, `randomValue`), which uses `math/rand/v2` **(review: v2 only)** | Identifiers must come from `crypto/rand` (NFR-5.1) |
| `testify` is banned; use the standard `testing` package with `github.com/google/go-cmp/cmp` | Google style: no assertion libraries |

Other structure rules:

* Each package has a `doc.go` with a package comment describing its responsibility. **(review, `revive` package-comments)**
* **Accept interfaces, return concrete types.** Constructors are `New(...) *T` or `New(...) (*T, error)`.
* Implementations assert their interface at compile time: `var _ store.MappingRepository = (*Repository)(nil)`.
* No `init()` functions (`gochecknoinits`), and no mutable package-level state (`gochecknoglobals`). Sentinel errors, compiled regexps and immutable lookup tables are allowed.
* Wiring and configuration happen only in `cmd/shadowmock`. Libraries never read environment variables or flags. **(review)**

## 6. Comments and documentation

* Every exported identifier and every package has a doc comment that starts with its name and is a full sentence ending in a period (`revive` exported, `godot`).
* Comments explain **why**, not what. Do not restate the code. **(review)**
* Reference requirements and design sections where the behaviour is non-obvious: `// Layer beats priority (FR-7.5).` **(review)**
* TODOs name an owner or an issue: `// TODO(peter): …` or `// TODO(#123): …`. A bare `// TODO` or `FIXME` is rejected by `scripts/lint.sh`.
* Deprecations use the standard `// Deprecated: …` paragraph.

## 7. Errors

* **Always handle errors** (`errcheck`, with `fmt.Fprint*` to `http.ResponseWriter` and `(*bytes.Buffer).Write` excluded).
* **Wrap with context** using `fmt.Errorf("load mappings for %s: %w", scope, err)`. Messages are lower case, have no trailing punctuation, and do not repeat "failed to" (`revive` error-strings).
* **Compare with `errors.Is` / `errors.As`**, never `==` or type assertions on wrapped errors (`errorlint`).
* **Handle an error once:** either return it (wrapped) or log it, never both. **(review)**
* **No panics in library code.** `panic` is allowed only in `Must*` helpers called at start-up and in tests (`forbidigo`, with test and `must.go` exclusions).
* Do not return `nil, nil` (`nilnil`), or `nil` error after a non-nil one (`nilerr`).
* **API errors [SM]:** handlers never write ad-hoc error bodies. They return an `*apierr.Error{Status, Code, Message}` that the admin middleware renders as `{"error":"<code>","message":"…"}`. Codes are the constants in `internal/apierr` and must match the error codes in the feature files (for example `org_mismatch`, `ip_not_allowed`, `egress_blocked`). **(review, acceptance specs)**

## 8. Context, cancellation and timeouts

* `ctx context.Context` is the first parameter of any function that does I/O, blocks or calls something that might (`revive` context-as-argument).
* Never store a context in a struct (`containedctx`). Never pass `nil`; use `context.TODO()` only temporarily.
* All outbound HTTP requests are built with `http.NewRequestWithContext` (`noctx`). Delays (`time.Timer`) and body reads stop on `ctx.Done()`.
* Context keys are unexported struct types (`type ctxKey struct{}`), as in `tenant.With`/`tenant.From` (`revive` context-keys-type).

## 9. Concurrency

* Every goroutine has an owner that can stop it and wait for it to finish (`errgroup`, or a `Close()`/`Shutdown(ctx)` method). There are no fire-and-forget goroutines on the request path except webhooks, which go through the bounded webhook worker pool. **(review)**
* Prefer `atomic.Pointer[T]` snapshots for read-mostly data (layers, detector index, org directory), as in DESIGN.md §12. Writers build a new value and swap it in.
* Mutexes are named fields, not embedded, and guard the fields declared immediately after them. Never copy a struct that holds a lock (`govet` copylocks).
* Channels are unbuffered or size 1 unless a larger buffer is justified in a comment (Uber guide). **(review)**
* All tests run with `-race` in CI.

## 10. Time and randomness [SM]

* **Never call `time.Now`, `time.Since`, `time.Until`, `time.After`, `time.Tick` or `time.Sleep` directly** outside `internal/clock` (`forbidigo`). Take a `clock.Clock` dependency so the test clock in the acceptance specs controls TTLs, retention and templating `now`.
* **Identifiers and secrets** (session keys, URL-based mock IDs, CI secrets) come only from `internal/ids`, which uses `crypto/rand` (`depguard`, `gosec` G404).
* Compare secrets in constant time with `crypto/subtle.ConstantTimeCompare`. **(review)**

## 11. Logging, metrics and tracing [SM]

* Use `log/slog` only, through a logger passed in or taken from context (`slog.InfoContext(ctx, …)`). Do not use `fmt.Print*` or `print` (`forbidigo`), and do not use the global `slog.Info` outside `cmd/` (`sloglint`).
* Use key-value pairs with **snake_case** keys and constant messages: `logger.InfoContext(ctx, "session created", "session_id", id, "kind", kind)` (`sloglint`: kv-only, snake key naming, static messages).
* **Never log secrets.** Session keys, JWTs, CI secrets and request bodies go through `redact.Key()` / `redact.Body()`. Session keys are shown as `smk_ABCD…` (security.feature). **(review, acceptance specs)**
* **Metric labels** may be `org`, `layer`, `stage`, `status` and `method`, but never session id, path, client IP or user (central_server.feature). The metrics package exposes typed constructors that accept only those labels. **(review)**

## 12. HTTP

* Servers set `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout` (above the maximum mock delay) and `IdleTimeout` (`gosec` G112).
* Request bodies are read once through `http.MaxBytesReader` with `limits.maxBodyBytes`. **(review)**
* There is **no `http.DefaultClient`, `http.Get`, `http.Post` or `http.Head`** (`forbidigo`). Outbound calls (proxying, webhooks, JWKS) use the shared `egress.Client`, which applies timeouts and the SSRF policy.
* Response bodies are always closed (`bodyclose`).

## 13. Data access

* SQL is always parameterised, with no string-built queries (`gosec` G201/G202). Every query takes a context.
* Rows are closed and `rows.Err()` is checked (`sqlclosecheck`, `rowserrcheck`).
* Every repository method takes a `tenant.ID` or `store.Scope` first after `ctx`, so queries cannot forget the org filter (DESIGN.md §1.3). **(review)**
* Migrations live in `migrations/` as numbered SQL files. Never edit a merged migration. **(review)**

## 14. JSON and the wire model [SM]

* Structs that are marshalled to JSON carry explicit `json` tags (`musttag`).
* WireMock-compatible and admin API JSON uses **camelCase** keys (`tagliatelle`). External formats that use snake_case (JWT claims such as `org_id`, OAuth fields) live in `internal/auth/claims.go`, which is excluded from that rule.
* Unknown fields in the WireMock model are preserved through `Extra map[string]json.RawMessage` (FR-1.4). Never use `DisallowUnknownFields` on mapping input. **(review)**

## 15. Performance

* The hot-path packages (`engine`, `match`, `detect`, `respond`) have benchmarks for their public entry points, run in the `@perf` pipeline. **(review)**
* No reflection or regexp compilation per request. Compile once on the `CompiledStub`. **(review)**
* Use `sync.Pool` for request body buffers. Preallocate slices and maps when the size is known (`prealloc`, `perfsprint`).
* Don't optimise without a benchmark showing the gain. **(review)**

## 16. Testing

* Use the standard `testing` package with `go-cmp` for comparisons: `if diff := cmp.Diff(want, got); diff != "" { t.Errorf("Resolve() mismatch (-want +got):\n%s", diff) }`.
* **Table-driven tests**, with a `name` field and `t.Run(tt.name, …)`. Failure messages say `Func(input) = got, want want` (Go Code Review Comments).
* Test helpers call `t.Helper()` (`thelper`). Use `t.Parallel()` where the test has no shared state (`tparallel` checks consistency). Use `t.Context()` for test contexts.
* Fixtures live in `testdata/`. Golden files update with `-update`.
* Fuzz tests (`FuzzXxx`) for every parser: mapping JSON, body scanner, template parser, CIDR input.
* Coverage targets are at least 85 % for `engine`, `match`, `detect` and `counter`, and at least 70 % overall (`scripts/lint.sh` reports, and CI enforces).
* **Acceptance specs:** the Gherkin features in `features/` run with godog. Step definitions live in `test/acceptance/steps_*.go` and reuse the vocabulary in `features/README.md`. A behaviour change updates the relevant `.feature` file in the same pull request.

## 17. Dependencies

* Prefer the standard library. Each new module needs a one-line justification in the pull request, and must have an acceptable licence (MIT, BSD, Apache-2.0 or ISC).
* Approved at design time:
  * `pgx/v5`, `go-redis/v9`, `aws-sdk-go-v2` (S3), `aymerick/raymond`, `petar-dambovaliev/aho-corasick`.
  * `golang-jwt/jwt/v5` with `MicahParks/keyfunc`, `fsnotify`, `go-cmp`, `godog`.
  * OpenTelemetry, `prometheus/client_golang`, `golang.org/x/sync`.
* `go.mod` and `go.sum` must be tidy (`go mod tidy -diff` in `lint.sh`), and `govulncheck` must pass.

## 18. Suppressing a lint finding

Only with a specific linter and a reason, on the line concerned:

```go
//nolint:gosec // G402: InsecureSkipVerify is required for the test-only upstream stub.
```

Blanket `//nolint` or file-wide suppressions are rejected (`nolintlint`). Project-wide exceptions belong in `.golangci.yml`, with a comment and reviewer approval.

## 19. Running the checks

```bash
make fmt      # gofumpt + goimports
make lint     # golangci-lint + scripts/lint.sh (file names, TODOs, go mod tidy, govulncheck)
make test     # go test -race ./... with coverage
```

CI runs the same targets on every pull request (`.github/workflows/ci.yml`), and a pull request cannot merge with lint findings.
