# Shadow Mock user guide

Shadow Mock is an HTTP mocking server. It reads WireMock's mapping files and speaks WireMock's
admin API, and adds shadow mocks so many tests can share one deployment safely.

Read the pages in order the first time, then use them as a reference.

| Page | What it covers |
|---|---|
| [1. Getting started](01-getting-started.md) | Install, run, first mapping, folder layout |
| [2. Matching requests](02-matching-requests.md) | URL, method, header, query, cookie and body matching, priority |
| [3. Building responses](03-responses.md) | Bodies, files, headers, delays, faults, gzip, proxying |
| [4. Templating](04-templating.md) | Handlebars templates and the helper reference |
| [5. Call-count responses](05-call-count-responses.md) | Different answers on the 1st, 2nd, 3rd call |
| [6. Shadow mocks](06-shadow-mocks.md) | Key-based and URL-based sessions, detection, override and mask |
| [7. Record and playback](07-record-and-playback.md) | Capture real traffic as mappings |
| [8. Webhooks](08-webhooks.md) | Calls made after a request is served |
| [9. Verification and debugging](09-verification-and-debugging.md) | Journal, unmatched requests, near misses |
| [10. Admin API](10-admin-api.md) | Endpoint reference |
| [11. Configuration](11-configuration.md) | Every setting, flags and environment variables |
| [12. Running a shared server](12-shared-server.md) | Separate admin port, IP allowlist, health checks |
| [13. Coming from WireMock](13-coming-from-wiremock.md) | What is the same, what differs |
| [14. Troubleshooting](14-troubleshooting.md) | Common problems and fixes |
| [15. Running in Docker](15-docker.md) | The container image, Compose example, health check |

## Terms

| Term | Meaning |
|---|---|
| **Mapping** (or stub) | A request pattern plus the response to give |
| **Global session** | The mappings every request can see. This is how WireMock behaves |
| **Shadow mock** | A private set of mappings that only some requests see |
| **Key-based shadow mock** | Selected by a session key found anywhere in the request |
| **URL-based shadow mock** | Selected by a base URL, `/d/<id>/...` |
| **Session** | The API's word for either kind of shadow mock |
| **Layer** | One set of mappings. The stack is key-based, then URL-based, then global |
