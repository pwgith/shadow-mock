@wiremock @P3 @FR-10.1 @FR-10.2
Feature: JSONPath that behaves like WireMock's
  WireMock evaluates matchesJsonPath with the Jayway JsonPath library. A mapping written for
  WireMock must match the same bodies here, including the filter operators and functions that
  the JSONPath standard does not have. Every expectation below was checked against a real
  WireMock 3.13.2.

  Background:
    Given Shadow Mock is running

  Scenario Outline: a filter applied to an object tests the object itself
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/jp",
          "bodyPatterns": [ { "matchesJsonPath": "<expression>" } ] },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/jp" with body:
      """json
      <body>
      """
    Then the response status is <status>

    Examples:
      | expression                    | body                   | status |
      | $[?(@.status == 'OPEN')]      | { "status": "OPEN" }   | 200    |
      | $[?(@.status == 'OPEN')]      | { "status": "CLOSED" } | 404    |
      | $[?(@.things.size() == 2)]    | { "things": [1, 2] }   | 200    |
      | $[?(@.things.size() == 2)]    | { "things": [1] }      | 404    |
      | $[?(@.n > 1 && @.n < 5)]      | { "n": 3 }             | 200    |
      | $[?(@.n > 1 && @.n < 5)]      | { "n": 9 }             | 404    |
      | $[?(@.n < 1 \|\| @.s == 'x')] | { "n": 3, "s": "x" }   | 200    |
      | $[?(!@.missing)]              | { "n": 3 }             | 200    |
      | $[?(@.flag == true)]          | { "flag": true }       | 200    |
      | $[?(@.gone == null)]          | { "gone": null }       | 200    |
      | $[?(@.gone == null)]          | { }                    | 404    |

  Scenario Outline: operators that only Jayway has
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/jp",
          "bodyPatterns": [ { "matchesJsonPath": "<expression>" } ] },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/jp" with body:
      """json
      <body>
      """
    Then the response status is <status>

    Examples:
      | expression                          | body                       | status |
      | $[?(@.sku =~ /a.*/i)]               | { "sku": "A100" }          | 200    |
      | $[?(@.sku =~ /a.*/)]                | { "sku": "A100" }          | 404    |
      | $[?(@.sku =~ /A/)]                  | { "sku": "A100" }          | 404    |
      | $[?(@.tier in ['gold','platinum'])] | { "tier": "gold" }         | 200    |
      | $[?(@.tier nin ['bronze'])]         | { "tier": "bronze" }       | 404    |
      | $[?(@.tags contains 'vip')]         | { "tags": ["new","vip"] }  | 200    |
      | $[?(@.name contains 'ada')]         | { "name": "ada lovelace" } | 200    |
      | $[?(@.tags anyof ['vip','staff'])]  | { "tags": ["staff"] }      | 200    |
      | $[?(@.tags noneof ['vip'])]         | { "tags": ["vip"] }        | 404    |
      | $[?(@.tags subsetof ['a','b','c'])] | { "tags": ["a","b"] }      | 200    |
      | $[?(@.tags subsetof ['a'])]         | { "tags": ["a","b"] }      | 404    |
      | $[?(@.items size 2)]                | { "items": [1, 2] }        | 200    |
      | $[?(@.word size 3)]                 | { "word": "abc" }          | 200    |
      | $[?(@.items empty true)]            | { "items": [] }            | 200    |
      | $[?(@.items empty false)]           | { "items": [] }            | 404    |

  Scenario Outline: a number and a string that reads as the same number are equal
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/jp",
          "bodyPatterns": [ { "matchesJsonPath": "$[?(@.code == '42')]" } ] },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/jp" with body:
      """json
      <body>
      """
    Then the response status is <status>

    Examples:
      | body             | status |
      | { "code": 42 }   | 200    |
      | { "code": "42" } | 200    |
      | { "code": 43 }   | 404    |

  Scenario Outline: path functions, indexes and scans
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/jp",
          "bodyPatterns": [ { "matchesJsonPath": "<expression>" } ] },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/jp" with body:
      """json
      <body>
      """
    Then the response status is <status>

    Examples:
      | expression                    | body                                         | status |
      | $.items.length()              | { "items": [1, 2] }                          | 200    |
      | $.items.length()              | { "other": 1 }                               | 404    |
      | $.items[-1]                   | { "items": [1, 2] }                          | 200    |
      | $.items[-1]                   | { "items": [] }                              | 404    |
      | $.items[0,1]                  | { "items": [1, 2] }                          | 200    |
      | $.items[1:]                   | { "items": [1, 2] }                          | 200    |
      | $.items[1:]                   | { "items": [1] }                             | 404    |
      | $..sku                        | { "a": { "b": [ { "sku": "X" } ] } }         | 200    |
      | $.items[?(@.price > $.limit)] | { "limit": 5, "items": [ { "price": 9 } ] }  | 200    |
      | $.items[?(@.price > $.limit)] | { "limit": 50, "items": [ { "price": 9 } ] } | 404    |
      | $.gone                        | { "gone": null }                             | 404    |
      | $.items                       | { "items": [] }                              | 404    |
      | $.name                        | { "name": "" }                               | 200    |

  Scenario Outline: expressions WireMock refuses are refused when the mapping is created
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "method": "POST", "urlPath": "/jp",
          "bodyPatterns": [ { "matchesJsonPath": "<expression>" } ] },
        "response": { "status": 200 } }
      """
    Then the response status is 422

    Examples:
      | expression        |
      | $a                |
      | $.                |
      | $..               |
      | $.items[?(@.n >)] |
      | $[?(@.n == 'x']   |

  Scenario Outline: functions at the end of a path print as WireMock prints them
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/fn" },
        "response": { "status": 200, "body": "{{jsonPath request.body '<expression>'}}", "transformers": ["response-template"] } }
      """
    When I send a "POST" request to "/fn" with body:
      """json
      { "items": [1, 2, 3, 4], "price": [1.5, 2.5], "big": [10000000, 2], "text": "abc" }
      """
    Then the response body equals '<printed>'

    Examples: aggregates give a Java double
      | expression         | printed |
      | $.items.sum()      | 10.0    |
      | $.items.avg()      | 2.5     |
      | $.items.min()      | 1.0     |
      | $.items.max()      | 4.0     |
      | $.price.sum()      | 4.0     |
      | $.price.max()      | 2.5     |
      | $.big.max()        | 1.0E7   |
      | $.items.stddev()   | 1.118033988749895 |

    Examples: the others print the value they find
      | expression         | printed |
      | $.items.length()   | 4       |
      | $.items.size()     | 4       |
      | $.items.first()    | 1       |
      | $.items.last()     | 4       |
      | $.items.index(1)   | 2       |

    Examples: a function with nothing to work on prints nothing
      | expression         | printed |
      | $.missing.sum()    |         |
      | $.text.sum()       |         |

  Scenario Outline: JsonUnit placeholders in equalToJson
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/ju",
          "bodyPatterns": [ { "equalToJson": <expected> } ] },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/ju" with body:
      """json
      <body>
      """
    Then the response status is <status>

    Examples:
      | expected                                      | body                | status |
      | { "id": "${json-unit.any-string}" }           | { "id": "x" }       | 200    |
      | { "id": "${json-unit.any-string}" }           | { "id": 1 }         | 404    |
      | { "id": "${json-unit.any-string}" }           | { "id": null }      | 404    |
      | { "n": "${json-unit.any-number}" }            | { "n": 3.5 }        | 200    |
      | { "n": "${json-unit.any-number}" }            | { "n": "3" }        | 404    |
      | { "b": "${json-unit.any-boolean}" }           | { "b": false }      | 200    |
      | { "b": "${json-unit.any-boolean}" }           | { "b": "false" }    | 404    |
      | { "id": "${json-unit.regex}[a-z]+-\\\\d+" }    | { "id": "ord-12" }  | 200    |
      | { "id": "${json-unit.regex}[a-z]+" }          | { "id": "ord-12" }  | 404    |
      | { "id": "${json-unit.ignore}" }               | { "id": null }      | 200    |
      | { "id": "${json-unit.ignore}", "a": 1 }       | { "a": 1 }          | 404    |
      | { "id": "${json-unit.ignore-element}", "a": 1 } | { "a": 1 }        | 200    |

  Scenario: equalToJson ignores an element marked ignore-element, present or not
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/ie",
          "bodyPatterns": [ { "equalToJson": { "id": 1, "note": "${json-unit.ignore-element}" } } ] },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/ie" with body:
      """json
      { "id": 1, "note": "anything" }
      """
    Then the response status is 200
    When I send a "POST" request to "/ie" with body:
      """json
      { "id": 1 }
      """
    Then the response status is 200
    When I send a "POST" request to "/ie" with body:
      """json
      { "id": 1, "extra": true }
      """
    Then the response status is 404
