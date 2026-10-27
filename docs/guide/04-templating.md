# 4. Templating

Response templating lets a response use data from the request. Shadow Mock uses Handlebars with
WireMock's helper set, so WireMock templates work unchanged.

## Turning it on

Templating is on for a mapping that lists the `response-template` transformer:

```json
{
  "request": { "method": "POST", "urlPath": "/echo" },
  "response": {
    "status": 200,
    "transformers": ["response-template"],
    "body": "Hello {{jsonPath request.body '$.name'}} from {{request.path}}"
  }
}
```

To template every response, set `templating.global: true` in the [configuration](11-configuration.md).

Output is not HTML-escaped, so `<` and `&` come through as written.

Templates can read only the data they are given. There is no file or network access from a
template.

## The request

| Expression | Value |
|---|---|
| `request.url` | The path and query string |
| `request.path` | The path |
| `request.pathSegments.[0]`, `request.pathSegments.0` | One path segment |
| `request.method` | The HTTP method |
| `request.query.name` | A query parameter. For repeated parameters, `request.query.name.0` is the first |
| `request.headers.Name` | A header |
| `request.cookies.name` | A cookie |
| `request.body` | The body as text |
| `request.baseUrl` | The scheme, host and port the client used |
| `request.clientIp` | The client's address |
| `request.callNumber` | How many times this mapping has served this scope, counting this call. See [Call-count responses](05-call-count-responses.md) |

Inside a path template, a path parameter is available as `request.path.name`.

## Helpers

### Dates and times

| Helper | Example |
|---|---|
| `now` | `{{now}}`, `{{now offset='3 days'}}`, `{{now offset='-1 hours'}}`, `{{now format='yyyy-MM-dd'}}`, `{{now timezone='Europe/London'}}`, `{{now format='epoch'}}` (milliseconds) or `'unix'` |
| `formatDate` | `{{formatDate request.query.d format='dd/MM/yyyy'}}` |
| `parseDate` | `{{parseDate request.query.d format='yyyy-MM-dd'}}` |
| `truncateDate` | `{{truncateDate (now) 'first day of month'}}` |

Date formats are Java-style patterns, such as `yyyy-MM-dd'T'HH:mm:ssXXX`.

### Random values

| Helper | Example |
|---|---|
| `randomValue` | `{{randomValue length=12 type='ALPHANUMERIC'}}`. Types: `ALPHANUMERIC`, `ALPHABETIC`, `NUMERIC`, `HEXADECIMAL`, `UUID`, `ALPHANUMERIC_AND_SYMBOLS` |
| `randomInt` | `{{randomInt lower=1 upper=100}}` |

### Strings and encoding

`base64`, `urlEncode`, `trim`, `lower`, `upper`, `capitalize`, `replace`, `substring`, `join`,
`size`, `regexExtract`, `regexGroups`.

```
{{regexExtract request.path '[0-9]+'}}
{{replace request.body 'secret' 'redacted'}}
{{base64 request.body}}
```

### Reading JSON and XML

| Helper | Use |
|---|---|
| `jsonPath` | `{{jsonPath request.body '$.items[0].id'}}` |
| `xPath` | `{{xPath request.body '/order/@id'}}` |
| `parseJson` | `{{parseJson request.body 'doc'}}` parses to a variable, then `{{doc.name}}` |
| `toJson`, `formatJson` | Print a value, or pretty-print JSON |
| `jsonMerge`, `jsonRemove`, `jsonArrayAdd` | Change a JSON document |

### Logic and maths

`eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `contains`, `and`, `or`, `not`, `math`, `array`, and the
Handlebars built-ins `if`, `unless` and `each`.

```
{{#if (eq request.method 'POST')}}created{{else}}ok{{/if}}
{{math request.callNumber '*' 10}}
```

## An example

```json
{
  "request": { "method": "POST", "urlPath": "/orders" },
  "response": {
    "status": 201,
    "transformers": ["response-template"],
    "headers": { "Content-Type": "application/json", "Location": "/orders/{{randomValue length=8 type='NUMERIC'}}" },
    "body": "{ \"customer\": \"{{jsonPath request.body '$.customer'}}\", \"created\": \"{{now}}\" }"
  }
}
```

## Errors

A template that fails to render (for example, a helper given the wrong data) returns a
`template_error` response and the cause is logged. Check the response body for the reason.
