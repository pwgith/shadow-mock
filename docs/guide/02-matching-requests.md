# 2. Matching requests

A mapping's `request` says which requests it answers. Every field you give must match. Fields you
leave out match anything.

```json
{
  "request": {
    "method": "POST",
    "urlPath": "/orders",
    "queryParameters": { "dryRun": { "equalTo": "false" } },
    "headers": { "Content-Type": { "contains": "json" } },
    "bodyPatterns": [ { "matchesJsonPath": "$.items[?(@.qty > 0)]" } ]
  },
  "response": { "status": 201 }
}
```

## URL

Use one of these:

| Field | Matches |
|---|---|
| `url` | The path and query string, exactly |
| `urlPath` | The path, exactly. The query string is ignored |
| `urlPattern` | The path and query string against a regular expression |
| `urlPathPattern` | The path against a regular expression |
| `urlPathTemplate` | A path template such as `/users/{id}`, with `pathParameters` to constrain the values |

```json
{ "request": { "urlPathTemplate": "/users/{id}", "pathParameters": { "id": { "matches": "[0-9]+" } } },
  "response": { "status": 200, "body": "user {{request.pathSegments.1}}" } }
```

## Method

`"method": "GET"`, or any other HTTP method. `"ANY"` matches every method.

## Headers, query parameters, cookies, form parameters

Each takes a map from name to a value matcher:

```json
{ "headers": { "Accept": { "equalTo": "application/json" } },
  "queryParameters": { "page": { "matches": "[0-9]+" } },
  "cookies": { "session": { "absent": true } },
  "formParameters": { "grant_type": { "equalTo": "client_credentials" } } }
```

Other request fields: `basicAuthCredentials` (`username` and `password`), `host`, `port` and
`scheme`.

## Value matchers

| Matcher | Meaning |
|---|---|
| `equalTo` | Exact match. Add `"caseInsensitive": true` to ignore case |
| `contains`, `doesNotContain` | Substring |
| `matches`, `doesNotMatch` | Regular expression |
| `absent` | The header, parameter or cookie is not present |
| `equalToNumber`, `greaterThanNumber`, `greaterThanEqualNumber`, `lessThanNumber`, `lessThanEqualNumber` | Numeric comparison |
| `before`, `after`, `equalToDateTime` | Date comparison. Options: `actualFormat`, `truncateExpected`, `truncateActual` |
| `hasExactly`, `includes` | For multi-valued headers and parameters |
| `and`, `or`, `not` | Combine other matchers |

```json
{ "queryParameters": { "q": { "and": [ { "contains": "mock" }, { "not": { "contains": "legacy" } } ] } } }
```

## Request bodies

`bodyPatterns` is a list, and every pattern in it must match.

| Pattern | Use |
|---|---|
| `equalTo`, `contains`, `matches`, `doesNotMatch` | Text |
| `binaryEqualTo` | Exact bytes, base64 encoded |
| `equalToJson` | JSON equality. Options: `ignoreArrayOrder` and `ignoreExtraElements`. Placeholders such as `${json-unit.ignore}` skip a value |
| `matchesJsonPath` | A JSONPath expression, or `{ "expression": "$.a", "equalTo": "x" }` to also test the value |
| `matchesJsonSchema` | A JSON Schema |
| `equalToXml` | XML equality. Supports the `${xmlunit.ignore}`, `${xmlunit.isNumber}`, `${xmlunit.isDateTime}` and `${xmlunit.matchesRegex}` placeholders, custom placeholder delimiters, `exemptedComparisons` and `exemptedComments` |
| `matchesXPath` | An XPath expression, with `xPathNamespaces` for prefixes and an optional sub-matcher |
| Multipart | `multipartPatterns`, matching each part's headers and body |

```json
{ "bodyPatterns": [ { "equalToJson": "{ \"id\": 1, \"ts\": \"${json-unit.ignore}\" }", "ignoreExtraElements": true } ] }
```

```json
{ "bodyPatterns": [ { "matchesXPath": "//a:order/a:id", "xPathNamespaces": { "a": "urn:example" } } ] }
```

## Custom matchers

A Go program that embeds Shadow Mock can register its own matchers by implementing
`extension.RequestMatcher` from `pkg/extension`. A mapping then selects one by name. This replaces
WireMock's Java extensions. See [features/01_wiremock_parity/request_matching/custom_matchers.feature](../../features/01_wiremock_parity/request_matching/custom_matchers.feature)
for the mapping syntax.

## Priority

When more than one mapping matches, the one with the **lowest `priority` number wins**
(`"priority": 1` beats `"priority": 5`). On a tie, the mapping added most recently wins.

Priority applies only within one layer. A shadow mock's mapping always beats a global mapping,
whatever their priorities. See [Shadow mocks](06-shadow-mocks.md).

## Identifying mappings

Give a mapping an `id` (a UUID) and a `name` so you can find, replace and delete it. Replacing a
mapping with the same id also resets its call counters.
