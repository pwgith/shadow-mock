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
`size`, `regexExtract`, `pickRandom` (`{{pickRandom 'red' 'green'}}`), `randomDecimal` (`{{randomDecimal lower=1 upper=2}}`),
`val` (`{{val request.query.id or='none'}}` gives the value, or the fallback when it is missing),
and `date` (the same as `formatDate`).

```
{{regexExtract request.path '[0-9]+'}}
{{replace request.body 'secret' 'redacted'}}
{{base64 request.body}}
```

`regexExtract` prints the first match. To use the capture groups of a pattern, give it a variable
name as a third argument. The groups are kept in that variable, and nothing is printed:

```
{{regexExtract 'order-123' '([a-z]+)-([0-9]+)' 'm'}}{{m.1}}-{{m.0}}
```

This prints `123-order`. `m.0` is the first group and `m.1` the second.

### Reading JSON and XML

| Helper | Use |
|---|---|
| `jsonPath` | `{{jsonPath request.body '$.items[0].id'}}` |
| `xPath` | `{{xPath request.body '/order/@id'}}` |
| `parseJson` | `{{parseJson request.body 'doc'}}` parses to a variable, then `{{doc.name}}` |
| `toJson`, `formatJson` | Print a value, or pretty-print JSON |
| `jsonMerge`, `jsonRemove`, `jsonArrayAdd` | Change a JSON document |

### Forms and SOAP

`formData` reads a url-encoded form into a variable, and `soapXPath` evaluates an XPath inside the body
of a SOAP envelope, naming elements by their local names:

```
{{formData request.body 'form' urlDecode=true}}Hello {{form.firstName}}
{{soapXPath request.body '/GetClaim/ClaimId/text()'}}
```

### Logic and maths

`eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `contains`, `and`, `or`, `not`, `matches`, `math`, `array`,
`arrayAdd`, `arrayJoin`, `range`, and the Handlebars built-ins `if`, `unless`, `each`, `with` and `lookup`.

`matches` tests the whole text against a regular expression, as a block or as an expression:
`{{#matches request.query.code '[0-9]+'}}numeric{{else}}no{{/matches}}`. `arrayAdd` returns a new list
with an item added at the end, or at the start with `position='start'`. `arrayJoin` joins a list with a
separator. `range` gives the whole numbers from one to another, both included.

```
{{#if (eq request.method 'POST')}}created{{else}}ok{{/if}}
{{math request.callNumber '*' 10}}
```

## Variables and block helpers

Some helpers use the text between their tags as input. `assign` stores that text under a name that the rest of the template reads:

```handlebars
{{#assign 'existing'}}[{"id": 1}]{{/assign}}
{{#jsonArrayAdd existing maxItems=2}}{"id": 2}{{/jsonArrayAdd}}
```

| Block helper | What it does |
|---|---|
| `{{#assign 'name'}}text{{/assign}}` | Stores the text, trimmed. It prints nothing |
| `{{#parseJson 'name'}}json{{/parseJson}}` | Parses the JSON into `name`. With no name it returns the parsed value |
| `{{#formatJson}}json{{/formatJson}}` | Lays JSON out as WireMock does (`"a" : 1`, arrays on one line). `format='compact'` removes the layout. Key order is kept |
| `{{#formatXml}}xml{{/formatXml}}` | Indents XML. `format='compact'` removes the layout |
| `{{#trim}}  text  {{/trim}}` | Removes white space from both ends |
| `{{#jsonArrayAdd list}}item{{/jsonArrayAdd}}` | Appends the item (JSON) to the array. Options: `maxItems=n` drops the oldest items, `flatten=true` adds an array's elements |
| `{{#jsonMerge base}}json{{/jsonMerge}}` | Merges the JSON over `base`. `removeNulls=true` drops null members |

`val` can set a variable too: `{{val request.query.tier.first or='basic' assign='tier'}}` prints nothing and keeps the value's type, so `{{#lt count 20}}` still compares numbers.

The comparison helpers (`eq`, `neq`, `lt`, `lte`, `gt`, `gte`, `and`, `or`, `not`) and `contains` and `matches` work as blocks with an optional `{{else}}`: `{{#gt count 3}}many{{else}}few{{/gt}}`.

## Partials and block parameters

A template can define a reusable piece of itself with `{{#*inline "name"}}…{{/inline}}` and use it
with `{{> name}}`. Pass a value to give the partial its own context, which it reads as `this`:

```
{{#*inline "item"}}{ "brand": {{toJson this.brand}} }{{/inline}}
{{#with (parseJson request.body) as |req|}}
{ "items": [ {{#each req.items as |it|}}{{> item it}}{{#unless @last}},{{/unless}}{{/each}} ] }
{{/with}}
```

Definitions can appear anywhere in the template and produce no output themselves. A partial can use
another partial. `as |name|` after `each` or `with` names the current value. `{{else}}` after an
`each` runs when there is nothing to loop over, and `@last` is true on the final pass.

`toJson` writes a value as JSON without escaping `<`, `>` or `&`, so `{{toJson name}}` gives
`"Ada & <Bob>"`.

Defaults are a common pattern in templates: `{{#if (neq this.age null)}}{{toJson this.age}}{{else}}5{{/if}}`
uses the request's `age` when there is one, and `5` when there is not.

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

A call to a helper that does not exist is an error, as it is in WireMock, so a typo in a helper name
is found and does not render as blank text.


A template that fails to render (for example, a helper given the wrong data) returns a
`template_error` response and the cause is logged. Check the response body for the reason.
