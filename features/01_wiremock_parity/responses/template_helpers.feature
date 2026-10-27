@wiremock @P3 @FR-3.4
Feature: Template helpers
  Every helper the user guide lists, with the example the guide gives.

  Background:
    Given Shadow Mock is running
    And the test clock is at "2026-11-01T09:30:00Z"

  Scenario Outline: Dates and times
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body matches "<pattern>"

    Examples:
      | template                                                  | pattern                       |
      | {{now format='epoch'}}                                    | ^[0-9]{13}$                   |
      | {{now format='unix'}}                                     | ^[0-9]{10}$                   |
      | {{now offset='-1 hours' format='HH:mm'}}                  | ^08:30$                       |
      | {{now offset='3 days' format='yyyy-MM-dd'}}               | ^2026-11-04$                  |
      | {{now timezone='Europe/London' format='HH:mm'}}           | ^09:30$                       |
      | {{now timezone='Asia/Tokyo' format='HH:mm'}}              | ^18:30$                       |
      | {{formatDate '2026-11-01T09:30:00Z' format='dd/MM/yyyy'}} | ^01/11/2026$                  |
      | {{parseDate '2026-11-01' format='yyyy-MM-dd'}}            | ^2026-11-01T00:00:00          |
      | {{truncateDate (now) 'first day of month'}}               | ^2026-11-01T00:00:00          |
      | {{truncateDate (now) 'first day of month' format='yyyy-MM-dd'}} | ^2026-11-01$            |

  Scenario Outline: Random values
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body matches "<pattern>"

    Examples:
      | template                                         | pattern                     |
      | {{randomValue length=12 type='ALPHANUMERIC'}}    | ^[A-Za-z0-9]{12}$           |
      | {{randomValue length=6 type='ALPHABETIC'}}       | ^[A-Za-z]{6}$               |
      | {{randomValue length=5 type='NUMERIC'}}          | ^[0-9]{5}$                  |
      | {{randomValue length=8 type='HEXADECIMAL'}}      | ^[0-9a-fA-F]{8}$            |
      | {{randomValue type='UUID'}}                      | ^[0-9a-f]{8}-[0-9a-f]{4}-   |
      | {{randomInt lower=1 upper=100}}                  | ^([1-9]\|[1-9][0-9]\|100)$  |

  Scenario Outline: Strings and encoding
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body is "<result>"

    Examples:
      | template                                    | result          |
      | {{base64 'hello'}}                          | aGVsbG8=        |
      | {{urlEncode 'a b&c'}}                       | a+b%26c         |
      | {{trim '  x  '}}                            | x               |
      | {{lower 'AbC'}}                             | abc             |
      | {{upper 'AbC'}}                             | ABC             |
      | {{capitalize 'hello'}}                      | Hello           |
      | {{replace 'a secret b' 'secret' 'redacted'}} | a redacted b   |
      | {{substring 'abcdef' 1 3}}                  | bc              |
      | {{size (array 1 2 3)}}                      | 3               |
      | {{regexExtract 'order-123-x' '[0-9]+'}}     | 123             |

  Scenario Outline: Logic and maths
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body is "<result>"

    Examples:
      | template                                                       | result  |
      | {{#if (eq request.method 'GET')}}yes{{else}}no{{/if}}          | yes     |
      | {{#if (neq request.method 'GET')}}yes{{else}}no{{/if}}         | no      |
      | {{#if (gt 3 2)}}yes{{else}}no{{/if}}                           | yes     |
      | {{#if (gte 2 2)}}yes{{else}}no{{/if}}                          | yes     |
      | {{#if (lt 3 2)}}yes{{else}}no{{/if}}                           | no      |
      | {{#if (lte 2 2)}}yes{{else}}no{{/if}}                          | yes     |
      | {{#if (contains 'abc' 'b')}}yes{{else}}no{{/if}}               | yes     |
      | {{#if (and true false)}}yes{{else}}no{{/if}}                   | no      |
      | {{#if (or true false)}}yes{{else}}no{{/if}}                    | yes     |
      | {{#if (not false)}}yes{{else}}no{{/if}}                        | yes     |
      | {{math 3 '*' 4}}                                               | 12      |
      | {{math 10 '-' 4}}                                              | 6       |
      | {{math request.callNumber '*' 10}}                             | 10      |
      | {{request.callNumber}}                                         | 1       |
      | {{#each (array 'a' 'b' 'c')}}{{this}}{{/each}}                 | abc     |
      | {{#unless false}}shown{{/unless}}                              | shown   |

  Scenario Outline: Reading and changing JSON
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "POST" request to "/h" with body:
      """json
      { "name": "Ann", "items": [ { "id": 7 }, { "id": 8 } ] }
      """
    Then the response body matches "<result>"

    Examples:
      | template                                                  | result      |
      | {{jsonPath request.body '$.items[0].id'}}                 | ^7$         |
      | {{parseJson request.body 'doc'}}{{doc.name}}              | ^Ann$       |
      | {{toJson (array 1 2)}}                                    | ^.1,2.$   |
      | {{jsonMerge '{\"a\":1}' '{\"b\":2}'}}                | ^{.a.:1,.b.:2}$ |
      | {{jsonRemove '{\"a\":1,\"b\":2}' '$.a'}}               | ^{.b.:2}$ |
      | {{jsonArrayAdd '[1]' 2}}                              | ^.1,2.$   |

  Scenario: Request details beyond the basics
    Given the global mapping:
      """json
      { "request": { "urlPathPattern": "/t/.*" },
        "response": { "status": 200, "transformers": ["response-template"],
          "body": "{{request.pathSegments.[0]}}|{{request.pathSegments.1}}|{{request.baseUrl}}|{{request.clientIp}}" } }
      """
    When I send a "GET" request to "/t/claims"
    Then the response body matches "^t\|claims\|http://127\.0\.0\.1:[0-9]+\|127\.0\.0\.1$"

  Scenario: A path template parameter is available as request.path.name
    Given the global mapping:
      """json
      { "request": { "urlPathTemplate": "/users/{id}", "pathParameters": { "id": { "matches": "[0-9]+" } } },
        "response": { "status": 200, "transformers": ["response-template"], "body": "user {{request.path.id}}" } }
      """
    When I send a "GET" request to "/users/42"
    Then the response body is "user 42"

  Scenario: A template that fails to render returns template_error
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "transformers": ["response-template"], "body": "{{math 'x' '*' 2}}" } }
      """
    When I send a "GET" request to "/h"
    Then the response status is 500
    And the response JSON at "$.error" is "template_error"

  Scenario: Templating can be switched on for every response
    Given Shadow Mock is running with configuration:
      """yaml
      templating: { global: true }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/h" }, "response": { "status": 200, "body": "path={{request.path}}" } }
      """
    When I send a "GET" request to "/h"
    Then the response body is "path=/h"

  Scenario: Output is not HTML-escaped
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/h" },
        "response": { "status": 200, "transformers": ["response-template"], "body": "{{request.body}}" } }
      """
    When I send a "POST" request to "/h" with body:
      """
      <a href="x">&</a>
      """
    Then the response body matches "^<a href=.x.>&</a>$"

  Scenario: regexExtract can keep its capture groups in a variable
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "transformers": ["response-template"],
          "body": "{{regexExtract 'order-123' '([a-z]+)-([0-9]+)' 'm'}}{{m.1}}-{{m.0}}" } }
      """
    When I send a "GET" request to "/h"
    Then the response body is "123-order"
