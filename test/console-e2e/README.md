# Console browser tests

Cucumber features for the embedded console (`/__console/`), driven by Playwright in Chromium against a real `shadowmock`
binary. They complement the Go scenarios in `features/03_standalone/console_and_cli.feature`, which have no browser and
play the console's calls; these click through the page itself.

What they cover: the journal and its near-miss analysis (including that a request carrying markup is shown as text and
never runs), the shadow mocks list (delete with its question, rename, lifetime, match tokens, masks, keep alive, the
"no global fallback" mark), counters, recordings, the dry run, and the token field of a central server.

Each step acts on the page as a person would, and checks the engine only through its admin API.

## Running

```bash
cd test/console-e2e
npm ci
npx playwright install chromium
npm test
```

* Without `SHADOWMOCK_BIN` the tests build the binary with `go build ./cmd/shadowmock`. Set it to use a build you
  already have.
* `HEADED=1 npm test` shows the browser.
* `CONSOLE_E2E_TAGS="@sessions" npm test` runs one area: `@page`, `@journal`, `@sessions`, `@counters`, `@recordings`,
  `@dryrun`, `@signin`.
* A failed scenario attaches a screenshot to the report.

The tests start two servers on free ports (a plain standalone one, and a central server that asks for an admin token)
and a small upstream server for the recordings. Each scenario starts with no mappings, shadow mocks or journal.
