@P1 @FR-4.1 @FR-4.2 @FR-4.3
Feature: Call-count responses
  A mapping can return a different response depending on how many times it
  has been called. This replaces WireMock scenarios for retries, polling,
  flaky dependencies and rate limits.

  Background:
    Given Shadow Mock is running

  Scenario: Retry until success
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/payments" },
        "response": { "status": 200, "jsonBody": { "status": "SETTLED" } },
        "callResponses": [ { "calls": "1-2", "response": { "status": 503 } } ] }
      """
    When I send 4 "POST" requests to "/payments"
    Then the response statuses in order are 503, 503, 200, 200

  Scenario: Polling until complete
    Given the global mapping:
      """json
      { "request": { "urlPath": "/jobs/7" },
        "response": { "status": 200, "jsonBody": { "state": "COMPLETE" } },
        "callResponses": [ { "calls": "1-3", "response": { "status": 200, "jsonBody": { "state": "PENDING" } } } ] }
      """
    When I send 5 "GET" requests to "/jobs/7"
    Then the JSON values at "$.state" in order are "PENDING", "PENDING", "PENDING", "COMPLETE", "COMPLETE"

  Scenario Outline: Each selector form
    Given the global mapping:
      """json
      { "request": { "urlPath": "/c" },
        "response": { "status": 200 },
        "callResponses": [ { <selector>, "response": { "status": 500 } } ] }
      """
    When I send 8 "GET" requests to "/c"
    Then the response statuses in order are <statuses>

    Examples:
      | selector           | statuses                                |
      | "calls": "3"       | 200, 200, 500, 200, 200, 200, 200, 200  |
      | "calls": "2-4"     | 200, 500, 500, 500, 200, 200, 200, 200  |
      | "calls": "6+"      | 200, 200, 200, 200, 200, 500, 500, 500  |
      | "every": 3         | 200, 200, 500, 200, 200, 500, 200, 200  |

  Scenario: The first matching rule wins
    Given the global mapping:
      """json
      { "request": { "urlPath": "/order" },
        "response": { "status": 200 },
        "callResponses": [
          { "calls": "3", "response": { "status": 418 } },
          { "every": 3, "response": { "status": 500 } },
          { "calls": "5+", "response": { "status": 429 } } ] }
      """
    When I send 7 "GET" requests to "/order"
    Then the response statuses in order are 200, 200, 418, 200, 429, 500, 429

  Scenario: Rule responses support everything a normal response does
    Given the file "__files/third.json":
      """json
      { "from": "file" }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/rich" },
        "response": { "status": 200, "body": "default" },
        "callResponses": [
          { "calls": "2", "response": { "status": 200, "fixedDelayMilliseconds": 300, "body": "slow" } },
          { "calls": "3", "response": { "status": 200, "bodyFileName": "third.json" } } ] }
      """
    When I send a "GET" request to "/rich"
    Then the response body is "default"
    When I send a "GET" request to "/rich"
    Then the response arrives after at least 300 ms
    And the response body is "slow"
    When I send a "GET" request to "/rich"
    Then the response JSON at "$.from" is "file"

  @P3 @FR-3.4
  Scenario: Templates can read the call number
    Given the global mapping:
      """json
      { "request": { "urlPath": "/attempt" },
        "response": { "status": 200, "body": "attempt {{request.callNumber}}", "transformers": ["response-template"] } }
      """
    When I send 3 "GET" requests to "/attempt"
    Then the response bodies in order are "attempt 1", "attempt 2", "attempt 3"

  Scenario: Only requests the mapping served are counted
    Given the global mapping:
      """json
      { "request": { "urlPath": "/only", "headers": { "X-Go": { "equalTo": "yes" } } },
        "response": { "status": 200 },
        "callResponses": [ { "calls": "2", "response": { "status": 500 } } ] }
      """
    When I send a "GET" request to "/only"
    And I send a request:
      | method | path  | header:X-Go |
      | GET    | /only | yes         |
    And I send a request:
      | method | path  | header:X-Go |
      | GET    | /only | yes         |
    Then the last response status is 500

  Scenario: Each mapping has its own counter
    Given the global mapping:
      """json
      { "request": { "urlPath": "/a" }, "response": { "status": 200 }, "callResponses": [ { "calls": "1", "response": { "status": 201 } } ] }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/b" }, "response": { "status": 200 }, "callResponses": [ { "calls": "1", "response": { "status": 201 } } ] }
      """
    When I send a "GET" request to "/a"
    And I send a "GET" request to "/b"
    Then both responses have status 201

  @standalone
  Scenario: Replacing a mapping resets its counter
    Given the global mapping:
      """json
      { "id": "eeeeeeee-0000-0000-0000-000000000001", "request": { "urlPath": "/r" }, "response": { "status": 200 },
        "callResponses": [ { "calls": "1", "response": { "status": 201 } } ] }
      """
    And I send a "GET" request to "/r"
    When I call admin "PUT" "/mappings/eeeeeeee-0000-0000-0000-000000000001" with:
      """json
      { "request": { "urlPath": "/r" }, "response": { "status": 200 }, "callResponses": [ { "calls": "1", "response": { "status": 201 } } ] }
      """
    And I send a "GET" request to "/r"
    Then the response status is 201

  Scenario: List, set and reset counters through the admin API
    Given the global mapping:
      """json
      { "id": "eeeeeeee-0000-0000-0000-000000000002", "request": { "urlPath": "/k" }, "response": { "status": 200 },
        "callResponses": [ { "calls": "10", "response": { "status": 500 } } ] }
      """
    And I send 2 "GET" requests to "/k"
    When I call admin "GET" "/counters"
    Then the response JSON at "$.counters[?(@.stubMappingId=='eeeeeeee-0000-0000-0000-000000000002')].count" is 2
    When I call admin "PUT" "/counters/eeeeeeee-0000-0000-0000-000000000002" with:
      """json
      { "count": 9 }
      """
    And I send a "GET" request to "/k"
    Then the response status is 500
    When I call admin "POST" "/counters/reset"
    Then admin "GET" "/counters" shows count 0 for stub "eeeeeeee-0000-0000-0000-000000000002"

  Scenario: Mappings without call rules never create counters
    Given the global mapping:
      """json
      { "request": { "urlPath": "/plain" }, "response": { "status": 200 } }
      """
    When I send 5 "GET" requests to "/plain"
    Then admin "GET" "/counters" lists no counter for "/plain"

  Scenario Outline: Invalid call rules are rejected
    When I create a session mapping with callResponses:
      """json
      [ <rule> ]
      """
    Then the response status is 422

    Examples:
      | rule                                                         |
      | { "calls": "0", "response": { "status": 500 } }              |
      | { "calls": "4-2", "response": { "status": 500 } }            |
      | { "calls": "x", "response": { "status": 500 } }              |
      | { "every": 1, "response": { "status": 500 } }                |
      | { "calls": "3", "every": 2, "response": { "status": 500 } }  |
      | { "calls": "3" }                                             |

  @saas @multi-instance
  Scenario: Call numbers are gap-free and unique across instances
    Given Shadow Mock is running in SaaS mode with 3 instances
    And the global mapping:
      """json
      { "request": { "urlPath": "/n" }, "response": { "status": 200, "body": "{{request.callNumber}}", "transformers": ["response-template"] } }
      """
    When 300 "GET" requests to "/n" are spread across the instances concurrently
    Then the response bodies are exactly the numbers 1 to 300, each once
