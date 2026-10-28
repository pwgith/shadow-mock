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
| `request.id` | The id of the request in the journal |
| `request.url` | The path and query string |
| `request.path` | The path |
| `request.path.[0]`, `request.path.0`, `request.pathSegments.[0]` | One path segment |
| `request.requestLine.method`, `.path`, `.url`, `.host`, `.port`, `.scheme`, `.baseUrl`, `.query`, `.pathSegments` | The request line, which has the same fields as the request |
| `request.method` | The HTTP method |
| `request.query.name` | A query parameter; a repeated one prints as its first value. `request.query.name.[1]` is the second, `.[last]` the last, and `{{size request.query.name}}` counts them |
| `{{lookup request.query 'ids[].1'}}` | The second value of a parameter named `ids[]` |
| `request.multipart` | `true` when the body is multipart |
| `request.parts.name.body`, `.bodyAsBase64`, `.binary`, `.name`, `.headers.Content-Type` | A part of a multipart body, by its name |
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
| `parseDate` | `{{parseDate request.query.d format='yyyy-MM-dd'}}`. With no format it reads ISO 8601, RFC 1123 (`Tue, 15 Nov 1994 08:12:31 GMT`) and asctime dates. `format='unix'` reads seconds and `format='epoch'` milliseconds |
| `truncateDate` | `{{truncateDate (now) 'first day of month'}}`. The truncations are `first second of minute`, `first minute of hour`, `first hour of day`, `first day of month`, `first day of next month`, `last day of month`, `first day of year`, `first day of next year` and `last day of year`; the `last` ones give the start of the last day |
| `dateFormat` | `{{dateFormat date 'yyyy-MM-dd'}}` or `format='...'`. The styles `full`, `long`, `medium` and `short` are US English |

Date formats are Java-style patterns, such as `yyyy-MM-dd'T'HH:mm:ssXXX`.

### Random values

| Helper | Example |
|---|---|
| `randomValue` | `{{randomValue length=12 type='ALPHANUMERIC'}}`. Types: `ALPHANUMERIC`, `ALPHABETIC`, `NUMERIC`, `HEXADECIMAL`, `UUID`, `ALPHANUMERIC_AND_SYMBOLS` |
| `randomInt` | `{{randomInt lower=1 upper=100}}` |

### Strings and encoding

`base64`, `urlEncode` (`decode=true` reverses it), `trim`, `lower`, `upper`, `replace`, `substring`, `join`,
`size`, `regexExtract`, `hostname`, `pickRandom` (`{{pickRandom 'red' 'green'}}`), `randomDecimal` (`{{randomDecimal lower=1 upper=2}}`),
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

The Handlebars.java string helpers are there too:

| Helper | Example | Prints |
|---|---|---|
| `capitalize` | `{{capitalize 'hello big world'}}` | `Hello Big World`. `fully=true` lower-cases the rest of each word |
| `capitalizeFirst` | `{{capitalizeFirst 'hELLO'}}` | `HELLO` |
| `abbreviate` | `{{abbreviate 'Handlebars is great' 10}}` | `Handleb...` |
| `center`, `ljust`, `rjust` | `{{rjust 'ab' size=5 pad='.'}}` | `...ab` |
| `cut` | `{{cut 'a-b-c' '-'}}` | `abc` |
| `defaultIfEmpty` | `{{defaultIfEmpty request.query.q 'none'}}` | The value, or the fallback when it is missing, empty, zero or false |
| `slugify` | `{{slugify 'Hello World, It is Me!'}}` | `hello-world-it-is-me` |
| `stringFormat` | `{{stringFormat 'Hi %s, you are %05d' 'Bob' 42}}` | Java `String.format` |
| `stripTags` | `{{stripTags '<b>bold</b>'}}` | `bold` |
| `wordWrap` | `{{wordWrap text 20}}` | Lines of at most 20 characters |
| `yesno` | `{{yesno flag yes='Y' no='N' maybe='?'}}` | `yes`, `no` or `maybe` by default; a missing value is `maybe` |

`numberFormat` formats a number with `DecimalFormat`: `{{numberFormat value 'pattern' 'locale'}}`. The pattern
is `integer`, `currency`, `percent` or a pattern such as `#,##0.00`, and it defaults to `#,##0.###`. The
locale is written like `de_DE`. Options `minimumIntegerDigits`, `maximumIntegerDigits`,
`minimumFractionDigits`, `maximumFractionDigits`, `groupingUsed` and `roundingMode` (`UP`, `DOWN`, `CEILING`,
`FLOOR`, `HALF_UP`, `HALF_DOWN`, `HALF_EVEN`) change the result. Rounding is half-even by default.

`isOdd` and `isEven` print `odd` or `even` for a number of that kind and nothing otherwise, so
`{{#if (isOdd n)}}` works. `{{stripes index 'a' 'b'}}` prints `a` for an even index and `b` for an odd one, for
alternating rows. `systemValue` reads an environment variable, but only one that is listed in
`templating.permittedSystemKeys`; any other key prints `[ERROR: Access to NAME is denied]`.

### Reading JSON and XML

| Helper | Use |
|---|---|
| `jsonPath` | `{{jsonPath request.body '$.items[0].id'}}`. A path that names one place gives its value, and any other path gives a list. An object prints indented and a list as compact JSON (`[1,2]`). When the path finds nothing the result is `default=`, or nothing |
| `xPath` | `{{xPath request.body '/order/@id'}}`. An element prints as indented markup, text and attributes as their value, and `count()` and the other functions as their result. Only the first match is used |
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
`arrayAdd`, `arrayRemove`, `arrayJoin`, `range`, and the Handlebars built-ins `if`, `unless`, `each`, `with` and `lookup`.

`matches` tests the whole text against a regular expression, as a block or as an expression:
`{{#matches request.query.code '[0-9]+'}}numeric{{else}}no{{/matches}}`. `arrayAdd` returns a new list
with an item added at the end, or at the start with `position='start'`. `arrayJoin` joins a list with a
separator, and `prefix=` and `suffix=` go around the whole result. `range` gives the whole numbers from one to another, both
included. `arrayRemove` returns a new list without the last item, or without the one at `position='start'`,
`'end'` or a number. A list prints the way Java prints it, `[1, 2, 3]`.

`math` works on decimal numbers as WireMock does. A division gives the result with as many decimals as the first
number has, rounded half up: `{{math 7 '/' 2}}` is `4`, `{{math 7.0 '/' 2}}` is `3.5` and `{{math 1 '/' 3}}` is `0`.
`+`, `-`, `*` and `%` are exact.

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
