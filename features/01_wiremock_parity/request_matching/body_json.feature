@wiremock @P0 @FR-2.5
Feature: JSON body matching

  Background:
    Given Shadow Mock is running

  Scenario Outline: equalToJson with and without leniency flags
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/j",
          "bodyPatterns": [ { "equalToJson": { "id": 1, "tags": ["a", "b"] },
                              "ignoreArrayOrder": <ignoreOrder>, "ignoreExtraElements": <ignoreExtra> } ] },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/j" with body:
      """json
      <body>
      """
    Then the response status is <status>

    Examples:
      | ignoreOrder | ignoreExtra | body                                    | status |
      | false       | false       | { "tags": ["a","b"], "id": 1 }          | 200    |
      | false       | false       | { "id": 1, "tags": ["b","a"] }          | 404    |
      | true        | false       | { "id": 1, "tags": ["b","a"] }          | 200    |
      | false       | false       | { "id": 1, "tags": ["a","b"], "x": 2 }  | 404    |
      | false       | true        | { "id": 1, "tags": ["a","b"], "x": 2 }  | 200    |

  Scenario Outline: equalToJson placeholders
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/ph",
          "bodyPatterns": [ { "equalToJson": { "id": "${json-unit.any-number}", "ref": "${json-unit.regex}[A-Z]{3}-\\d+", "note": "${json-unit.ignore}" } } ] },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/ph" with body:
      """json
      <body>
      """
    Then the response status is <status>

    Examples:
      | body                                            | status |
      | { "id": 7, "ref": "ABC-12", "note": "anything" } | 200    |
      | { "id": "7", "ref": "ABC-12", "note": null }     | 404    |
      | { "id": 7, "ref": "ab-12", "note": "x" }         | 404    |

  Scenario Outline: matchesJsonPath presence, filters and sub-matchers
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/jp", "bodyPatterns": [ <pattern> ] }, "response": { "status": 200 } }
      """
    When I send a "POST" request to "/jp" with body:
      """json
      { "claim": { "id": 42, "status": "OPEN", "items": [ { "sku": "A1", "qty": 2 } ] } }
      """
    Then the response status is <status>

    Examples:
      | pattern                                                                                  | status |
      | { "matchesJsonPath": "$.claim.id" }                                                       | 200    |
      | { "matchesJsonPath": "$.claim.missing" }                                                  | 404    |
      | { "matchesJsonPath": "$.claim.items[?(@.qty > 1)]" }                                      | 200    |
      | { "matchesJsonPath": { "expression": "$.claim.status", "equalTo": "OPEN" } }              | 200    |
      | { "matchesJsonPath": { "expression": "$.claim.status", "matches": "CLOSED\|REJECTED" } }  | 404    |
      | { "matchesJsonPath": { "expression": "$.claim.items", "equalToJson": [ { "sku": "A1", "qty": 2 } ] } } | 200 |

  Scenario: matchesJsonSchema validates the request body
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/schema",
          "bodyPatterns": [ { "matchesJsonSchema": { "type": "object", "required": ["id"],
                                                     "properties": { "id": { "type": "integer" } } } } ] },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/schema" with body:
      """json
      { "id": 3 }
      """
    Then the response status is 200
    When I send a "POST" request to "/schema" with body:
      """json
      { "id": "three" }
      """
    Then the response status is 404

  Scenario: Several body patterns must all match
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/all",
          "bodyPatterns": [ { "matchesJsonPath": "$.a" }, { "contains": "\"b\"" } ] },
        "response": { "status": 200 } }
      """
    When I send a "POST" request to "/all" with body:
      """json
      { "a": 1 }
      """
    Then the response status is 404
