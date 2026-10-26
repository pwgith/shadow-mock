@wiremock @P0 @FR-2.4
Feature: String value matchers
  The same value matchers are used for headers, query parameters, cookies,
  form parameters and path parameters. They are exercised here through a
  header, X-Value.

  Background:
    Given Shadow Mock is running

  Scenario Outline: Each value matcher
    Given the global mapping:
      """json
      { "request": { "urlPath": "/v", "headers": { "X-Value": <pattern> } }, "response": { "status": 200 } }
      """
    When I send a request:
      | method | path | header:X-Value |
      | GET    | /v   | <value>        |
    Then the response status is <status>

    Examples:
      | pattern                                                    | value        | status |
      | { "equalTo": "abc" }                                       | abc          | 200    |
      | { "equalTo": "abc" }                                       | ABC          | 404    |
      | { "equalTo": "abc", "caseInsensitive": true }              | ABC          | 200    |
      | { "contains": "bc" }                                       | abcd         | 200    |
      | { "doesNotContain": "bc" }                                 | abcd         | 404    |
      | { "matches": "^a.c$" }                                     | abc          | 200    |
      | { "matches": "^a.c$" }                                     | abcc         | 404    |
      | { "doesNotMatch": "^a.c$" }                                | xyz          | 200    |
      | { "before": "2026-01-01T00:00:00Z" }                       | 2025-12-31T23:59:59Z | 200 |
      | { "after": "2026-01-01T00:00:00Z" }                        | 2025-12-31T23:59:59Z | 404 |
      | { "equalToDateTime": "2026-01-01T00:00:00Z" }              | 2026-01-01T00:00:00Z | 200 |
      | { "and": [ { "contains": "a" }, { "contains": "z" } ] }    | abz          | 200    |
      | { "and": [ { "contains": "a" }, { "contains": "z" } ] }    | abc          | 404    |
      | { "or": [ { "equalTo": "a" }, { "equalTo": "b" } ] }       | b            | 200    |
      | { "not": { "equalTo": "a" } }                              | b            | 200    |
      | { "not": { "equalTo": "a" } }                              | a            | 404    |

  Scenario: Relative date-time matching uses the current time
    Given the test clock is at "2026-11-01T09:00:00Z"
    And the global mapping:
      """json
      { "request": { "urlPath": "/d", "headers": { "X-Value": { "after": "now -1 days" } } }, "response": { "status": 200 } }
      """
    When I send a request:
      | method | path | header:X-Value       |
      | GET    | /d   | 2026-10-31T12:00:00Z |
    Then the response status is 200

  Scenario: Date-time matching with a custom format
    Given the global mapping:
      """json
      { "request": { "urlPath": "/df", "headers": { "X-Value": { "before": "2026-01-01T00:00:00Z", "actualFormat": "dd/MM/yyyy" } } },
        "response": { "status": 200 } }
      """
    When I send a request:
      | method | path | header:X-Value |
      | GET    | /df  | 31/12/2025     |
    Then the response status is 200
