@wiremock @P3 @FR-10.4 @FR-10.5
Feature: Template helpers and how they print their values
  The helper set and the way values are printed follow WireMock 3.13.2. Every expectation below
  was checked against a real WireMock.

  Background:
    Given Shadow Mock is running

  Scenario Outline: Text helpers
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body equals '<body>'

    Examples:
      | template                                          | body            |
      | {{abbreviate 'Handlebars is great' 10}}            | Handleb...      |
      | {{capitalize 'hello big world'}}                   | Hello Big World |
      | {{capitalize 'hELLO wORLD' fully=true}}            | Hello World     |
      | {{capitalizeFirst 'hELLO wORLD'}}                  | HELLO wORLD     |
      | [{{center 'ab' size=7 pad='*'}}]                   | [**ab***]       |
      | [{{ljust 'ab' size=5 pad='.'}}]                    | [ab...]         |
      | [{{rjust 'ab' size=5 pad='.'}}]                    | [...ab]         |
      | {{cut 'a-b-c' '-'}}                                | abc             |
      | {{defaultIfEmpty '' 'fallback'}}                   | fallback        |
      | {{slugify 'Hello World, It is Me!'}}               | hello-world-it-is-me |
      | {{stringFormat 'Hi %s, you are %05d' 'Bob' 42}}    | Hi Bob, you are 00042 |
      | {{stripTags '<b>bold</b> and <i>it</i>'}}          | bold and it     |
      | {{yesno true}} {{yesno false}} {{yesno nothing}}   | yes no maybe    |
      | {{yesno true yes='Y' no='N'}}                      | Y               |
      | {{urlEncode 'a b&c'}}                              | a+b%26c         |
      | {{urlEncode 'a%20b%26c' decode=true}}              | a b&c           |

  Scenario: wordWrap puts words on lines of at most the width
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "{{wordWrap 'aaa bbb ccc ddd eee' 8}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body is:
      """
      aaa bbb
      ccc ddd
      eee
      """

  Scenario Outline: Numbers print like Java's BigDecimal and DecimalFormat
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body equals '<body>'

    Examples:
      | template                                              | body        |
      | {{math 7 '/' 2}}                                       | 4           |
      | {{math 7.0 '/' 2}}                                     | 3.5         |
      | {{math 1 '/' 3}}                                       | 0           |
      | {{math 0.1 '+' 0.2}}                                   | 0.3         |
      | {{math 7 '%' 3}}                                       | 1           |
      | {{numberFormat 1234.5678}}                             | 1,234.568   |
      | {{numberFormat 0.256 'percent'}}                       | 26%         |
      | {{numberFormat 1234.5 'currency'}}                     | $1,234.50   |
      | {{numberFormat 1234.5 'integer'}}                      | 1,234       |
      | {{numberFormat 3.1 '000.000'}}                         | 003.100     |
      | {{numberFormat 1234.5678 '#,##0.00'}}                  | 1,234.57    |
      | {{numberFormat 1234.5 '#,##0.00' 'de'}}                | 1.234,50    |
      | {{numberFormat 1234567 groupingUsed=false}}            | 1234567     |
      | {{numberFormat 2.5 '0' roundingMode='UP'}}             | 3           |
      | {{numberFormat 2.5 '0'}}                               | 2           |
      | {{numberFormat 5 minimumIntegerDigits=3}}              | 005         |
      | {{isOdd 3}}-{{isEven 3}}-{{isEven 4}}                  | odd--even   |
      | {{stripes 0 'a' 'b'}}{{stripes 1 'a' 'b'}}             | ab          |

  Scenario Outline: Lists print as Java prints them
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body equals '<body>'

    Examples:
      | template                                                       | body          |
      | {{array 1 'a' true}}                                            | [1, a, true]  |
      | {{range 1 4}}                                                   | [1, 2, 3, 4]  |
      | {{arrayAdd (array 1 2) 9 position='start'}}                     | [9, 1, 2]     |
      | {{arrayAdd (array 1 2 3) 9 position=1}}                         | [1, 9, 2, 3]  |
      | {{arrayRemove (array 1 2 3)}}                                   | [1, 2]        |
      | {{arrayRemove (array 1 2 3) position='start'}}                  | [2, 3]        |
      | {{arrayRemove (array 1 2 3) position=1}}                        | [1, 3]        |
      | {{arrayJoin ',' (array 1 2 3) prefix='[' suffix=']'}}           | [1,2,3]       |
      | {{#each (range 1 3)}}<{{this}}>{{/each}}                        | <1><2><3>     |
      | {{size (range 3 6)}}                                            | 4             |

  Scenario Outline: Values from JSON and XML documents
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "POST" request to "/h" with body:
      """
      <input>
      """
    Then the response body equals '<body>'

    Examples:
      | input                              | template                                           | body       |
      | {"items":[{"id":1},{"id":2}]}      | {{jsonPath request.body '$.items[*].id'}}          | [1,2]      |
      | {"items":[{"id":1},{"id":2}]}      | {{jsonPath request.body '$.items[?(@.id>1)].id'}}  | [2]        |
      | {"a":"x"}                          | {{jsonPath request.body '$.a'}}                    | x          |
      | {"a":[1,2]}                        | {{jsonPath request.body '$.a'}}                    | [1,2]      |
      | {"a":1}                            | [{{jsonPath request.body '$.zz'}}]                 | []         |
      | {"a":1}                            | {{jsonPath request.body '$.zz' default='none'}}    | none       |
      | {"a":1}                            | [{{jsonPath request.body '$.items[*]'}}]           | [[]]       |
      | <a><b x="1">t</b><b>u</b></a>      | {{xPath request.body '/a/b'}}                      | <b x="1">t</b>  |
      | <a><b x="1">t</b><b>u</b></a>      | {{xPath request.body '/a/b/text()'}}               | t          |
      | <a><b x="1">t</b><b>u</b></a>      | {{xPath request.body '/a/b/@x'}}                   | 1          |
      | <a><b x="1">t</b><b>u</b></a>      | {{xPath request.body 'count(/a/b)'}}               | 2          |

  Scenario: jsonPath prints an object indented
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "{{jsonPath request.body '$.o'}}", "transformers": ["response-template"] } }
      """
    When I send a "POST" request to "/h" with body:
      """
      {"o":{"k":1,"l":[1,2]}}
      """
    Then the response body is:
      """
      {
        "k" : 1,
        "l" : [ 1, 2 ]
      }
      """

  Scenario Outline: Dates
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body equals '<body>'

    Examples:
      | template                                                                                     | body                 |
      | {{parseDate 'Tue, 15 Nov 1994 08:12:31 GMT'}}                                                 | 1994-11-15T08:12:31Z |
      | {{parseDate 'Sun Nov  6 08:49:37 1994'}}                                                      | 1994-11-06T08:49:37Z |
      | {{parseDate '86400' format='unix'}}                                                           | 1970-01-02T00:00:00Z |
      | {{parseDate '86400000' format='epoch'}}                                                       | 1970-01-02T00:00:00Z |
      | {{truncateDate (parseDate '2020-03-04T05:06:07Z') 'first day of month'}}                      | 2020-03-01T00:00:00Z |
      | {{truncateDate (parseDate '2020-03-04T05:06:07Z') 'first day of next month'}}                 | 2020-04-01T00:00:00Z |
      | {{truncateDate (parseDate '2020-03-04T05:06:07Z') 'last day of month'}}                       | 2020-03-31T00:00:00Z |
      | {{truncateDate (parseDate '2020-03-04T05:06:07Z') 'first day of next year'}}                  | 2021-01-01T00:00:00Z |
      | {{truncateDate (parseDate '2020-03-04T05:06:07Z') 'last day of year'}}                        | 2020-12-31T00:00:00Z |
      | {{truncateDate (parseDate '2020-03-04T05:06:07Z') 'first minute of hour'}}                    | 2020-03-04T05:00:00Z |
      | {{truncateDate (parseDate '2020-03-04T05:06:07Z') 'first second of minute'}}                  | 2020-03-04T05:06:00Z |
      | {{truncateDate (parseDate '2020-03-04T05:06:07Z') 'first hour of day'}}                       | 2020-03-04T00:00:00Z |
      | {{dateFormat (parseDate '2020-01-02T03:04:05Z') format='yyyy/MM/dd'}}                         | 2020/01/02           |
      | {{dateFormat (parseDate '2020-01-02T03:04:05Z') format='HH:mm'}}                              | 03:04                |

  Scenario: hostname is the name of the machine
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "{{hostname}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body matches ".+"

  Scenario: truncateDate refuses a truncation WireMock does not have
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "{{truncateDate (parseDate '2020-03-04T05:06:07Z') 'last hour of day'}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response status is 500

  Scenario: systemValue is denied unless the key is permitted
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "{{systemValue type='ENVIRONMENT' key='SHADOWMOCK_SECRET'}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body equals '[ERROR: Access to SHADOWMOCK_SECRET is denied]'

  Scenario: systemValue reads a permitted environment variable (PATH is set wherever the suite runs)
    Given Shadow Mock is running with configuration:
      """yaml
      templating:
        permittedSystemKeys: [PATH]
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "{{systemValue type='ENVIRONMENT' key='PATH'}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body matches ".+"
