@key-based @P1 @FR-7.3
Feature: Session-scoped admin API behaviour
  The session endpoints do more than answer 200: each one changes or reads
  only the session it names, and never the global session or another session.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPath": "/a" }, "response": { "status": 200, "body": "global" } }
      """
    And a key-based shadow mock "S1"
    And a key-based shadow mock "S2"

  Scenario: Get, replace and delete one session mapping by id
    Given the mapping in "S1":
      """json
      { "id": "aaaaaaaa-0000-0000-0000-000000000001", "request": { "urlPath": "/a" }, "response": { "status": 200, "body": "one" } }
      """
    When I call admin "GET" "/sessions/${S1.id}/mappings/aaaaaaaa-0000-0000-0000-000000000001"
    Then the response status is 200
    And the response JSON at "$.response.body" is "one"
    When I call admin "PUT" "/sessions/${S1.id}/mappings/aaaaaaaa-0000-0000-0000-000000000001" with:
      """json
      { "id": "aaaaaaaa-0000-0000-0000-000000000001", "request": { "urlPath": "/a" }, "response": { "status": 200, "body": "two" } }
      """
    Then the response status is 200
    And a "GET" request to "/a" with key "${S1.key}" returns body "two"
    When I call admin "DELETE" "/sessions/${S1.id}/mappings/aaaaaaaa-0000-0000-0000-000000000001"
    Then the response status is 200
    And admin "GET" "/sessions/${S1.id}/mappings/aaaaaaaa-0000-0000-0000-000000000001" returns status 404
    And a "GET" request to "/a" with key "${S1.key}" returns body "global"

  Scenario: A session mapping cannot be read, changed or deleted through another session
    Given the mapping in "S1":
      """json
      { "id": "aaaaaaaa-0000-0000-0000-000000000001", "request": { "urlPath": "/a" }, "response": { "status": 200, "body": "one" } }
      """
    Then admin "GET" "/sessions/${S2.id}/mappings/aaaaaaaa-0000-0000-0000-000000000001" returns status 404
    When I call admin "DELETE" "/sessions/${S2.id}/mappings/aaaaaaaa-0000-0000-0000-000000000001"
    Then the response status is 404
    And a "GET" request to "/a" with key "${S1.key}" returns body "one"

  Scenario: Deleting all of a session's mappings leaves the global and other sessions' mappings
    Given the mapping in "S1":
      """json
      { "request": { "urlPath": "/b" }, "response": { "status": 200, "body": "b1" } }
      """
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/c" }, "response": { "status": 200, "body": "c1" } }
      """
    And the mapping in "S2":
      """json
      { "request": { "urlPath": "/b" }, "response": { "status": 200, "body": "b2" } }
      """
    When I call admin "DELETE" "/sessions/${S1.id}/mappings"
    Then the response status is 200
    And admin "GET" "/sessions/${S1.id}/mappings" returns 0 mappings
    And admin "GET" "/sessions/${S2.id}/mappings" returns 1 mappings
    And a "GET" request to "/b" with key "${S2.key}" returns body "b2"
    And a "GET" request to "/a" returns body "global"

  Scenario: Import adds mappings to the session only
    When I call admin "POST" "/sessions/${S1.id}/mappings/import" with:
      """json
      { "mappings": [
          { "request": { "urlPath": "/b" }, "response": { "status": 200, "body": "b1" } },
          { "request": { "urlPath": "/c" }, "response": { "status": 200, "body": "c1" } } ] }
      """
    Then the response status is 200
    And admin "GET" "/sessions/${S1.id}/mappings" returns 2 mappings
    And admin "GET" "/sessions/${S2.id}/mappings" returns 0 mappings
    And a "GET" request to "/c" with key "${S1.key}" returns body "c1"
    And a "GET" request to "/c" returns status 404

  Scenario: Count, find and remove cover only the session's own requests
    Given I send a "GET" request to "/a" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    And I send a "GET" request to "/a" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    And I send a "GET" request to "/a" with header "X-Shadow-Mock-Key" set to "${S2.key}"
    When I call admin "POST" "/sessions/${S1.id}/requests/count" with JSON '{"method":"GET","urlPath":"/a"}'
    Then the response JSON at "$.count" is 2
    When I call admin "POST" "/sessions/${S1.id}/requests/find" with JSON '{"method":"GET","urlPath":"/a"}'
    Then the response JSON at "$.requests" has 2 items
    When I call admin "POST" "/sessions/${S1.id}/requests/remove" with JSON '{"method":"GET","urlPath":"/a"}'
    Then the response status is 200
    And admin "GET" "/sessions/${S1.id}/requests" returns 0 requests
    And admin "GET" "/sessions/${S2.id}/requests" returns 1 request

  Scenario: Unmatched requests and near misses are kept per session
    Given the mapping in "S1":
      """json
      { "name": "close", "request": { "method": "GET", "urlPath": "/orders/7" }, "response": { "status": 200 } }
      """
    And I send a "GET" request to "/orders/8" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    When I call admin "GET" "/sessions/${S1.id}/requests/unmatched"
    Then the response JSON at "$.requests" has 1 items
    When I call admin "GET" "/sessions/${S2.id}/requests/unmatched"
    Then the response JSON at "$.requests" has 0 items
    When I call admin "GET" "/sessions/${S1.id}/requests/unmatched/near-misses"
    Then the response JSON at "$.nearMisses[0].stubMapping.name" is "close"

  Scenario: Near misses for a request look through the session's layer stack
    Given the mapping in "S1":
      """json
      { "name": "close", "request": { "method": "GET", "urlPath": "/orders/7" }, "response": { "status": 200 } }
      """
    When I call admin "POST" "/sessions/${S1.id}/near-misses/request" with JSON '{"method":"GET","url":"/orders/8"}'
    Then the response JSON at "$.nearMisses[0].stubMapping.name" is "close"
    When I call admin "POST" "/sessions/${S2.id}/near-misses/request" with JSON '{"method":"GET","url":"/orders/8"}'
    Then the response body does not contain "close"

  Scenario: A session's call counters can be listed, set and reset without touching others
    Given the global mapping:
      """json
      { "id": "eeeeeeee-0000-0000-0000-000000000001", "request": { "urlPath": "/poll" }, "response": { "status": 200 },
        "callResponses": [ { "calls": "1-2", "response": { "status": 503 } } ] }
      """
    And I send 2 "GET" requests to "/poll" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    When I call admin "GET" "/sessions/${S1.id}/counters"
    Then the response JSON at "$.counters[?(@.stubMappingId=='eeeeeeee-0000-0000-0000-000000000001')].count" is 2
    When I call admin "PUT" "/sessions/${S1.id}/counters/eeeeeeee-0000-0000-0000-000000000001" with JSON '{"count":0}'
    Then the response status is 200
    And a "GET" request to "/poll" with key "${S1.key}" returns status 503
    When I call admin "PUT" "/sessions/${S1.id}/counters/eeeeeeee-0000-0000-0000-000000000001" with JSON '{"count":5}'
    Then a "GET" request to "/poll" with key "${S1.key}" returns status 200
    When I call admin "DELETE" "/sessions/${S1.id}/counters/eeeeeeee-0000-0000-0000-000000000001"
    Then the response status is 200
    And a "GET" request to "/poll" with key "${S1.key}" returns status 503
    When I call admin "GET" "/sessions/${S2.id}/counters"
    Then the response JSON at "$.counters" has 0 items

  Scenario: A session reports its own recording status
    When I call admin "GET" "/sessions/${S1.id}/recordings/status"
    Then the response status is 200
    And the response JSON at "$.status" is "NeverStarted"
