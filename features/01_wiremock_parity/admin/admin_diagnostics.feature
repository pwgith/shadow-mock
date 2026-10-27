@wiremock @standalone @P1 @FR-6.3 @FR-6.4
Feature: Admin diagnostics
  Endpoints that help an author check a mapping, find out why a request was
  answered the way it was, and see how the server is doing.

  Background:
    Given Shadow Mock is running

  Scenario: Validate says whether a mapping is acceptable without saving it
    When I call admin "POST" "/mappings/validate" with:
      """json
      { "request": { "urlPath": "/ok" }, "response": { "status": 200 } }
      """
    Then the response status is 200
    And the response JSON at "$.valid" is "true"
    And admin "GET" "/mappings" returns 0 mappings

  Scenario: Validate reports what is wrong with a mapping
    When I call admin "POST" "/mappings/validate" with:
      """json
      { "request": { "urlPath": "/bad" }, "response": { "status": 200 }, "scenarioName": "s" }
      """
    Then the response status is 200
    And the response JSON at "$.valid" is "false"
    And the response JSON at "$.errors" has 1 items

  Scenario: Dry run shows which mapping would answer without sending the request
    Given the global mapping:
      """json
      { "name": "pay", "request": { "urlPath": "/pay" }, "response": { "status": 402, "body": "declined" } }
      """
    When I call admin "POST" "/mappings/dry-run" with:
      """json
      { "request": { "method": "GET", "url": "/pay" } }
      """
    Then the response JSON at "$.matched" is "true"
    And the response JSON at "$.layer" is "global"
    And the response JSON at "$.stubMapping.name" is "pay"
    And the response JSON at "$.response.status" is 402
    And admin "GET" "/requests" returns 0 requests

  Scenario: Dry run reports near misses when nothing matches
    Given the global mapping:
      """json
      { "name": "pay", "request": { "urlPath": "/pay" }, "response": { "status": 200 } }
      """
    When I call admin "POST" "/mappings/dry-run" with:
      """json
      { "request": { "method": "GET", "url": "/pya" } }
      """
    Then the response JSON at "$.matched" is "false"
    And the response JSON at "$.nearMisses[0].stubMapping.name" is "pay"

  Scenario: Dry run resolves through a key-based shadow mock
    Given the global mapping:
      """json
      { "request": { "urlPath": "/pay" }, "response": { "status": 200, "body": "global" } }
      """
    And a key-based shadow mock "S1"
    And the mapping in "S1":
      """json
      { "name": "session-pay", "request": { "urlPath": "/pay" }, "response": { "status": 402 } }
      """
    When I call admin "POST" "/mappings/dry-run" with:
      """json
      { "sessionId": "${S1.id}", "request": { "method": "GET", "url": "/pay" } }
      """
    Then the response JSON at "$.layer" is "key-based"
    And the response JSON at "$.stubMapping.name" is "session-pay"
    When I call admin "POST" "/mappings/dry-run" with:
      """json
      { "request": { "method": "GET", "url": "/pay" } }
      """
    Then the response JSON at "$.layer" is "global"

  Scenario: Dry run for an unknown session is a 404
    When I call admin "POST" "/mappings/dry-run" with:
      """json
      { "sessionId": "ses_00000000000000000000000000", "request": { "method": "GET", "url": "/pay" } }
      """
    Then the response status is 404

  Scenario: Mappings that no request has matched are listed
    Given the global mapping:
      """json
      { "name": "used", "request": { "urlPath": "/used" }, "response": { "status": 200 } }
      """
    And the global mapping:
      """json
      { "name": "idle", "request": { "urlPath": "/idle" }, "response": { "status": 200 } }
      """
    And I send a "GET" request to "/used"
    When I call admin "GET" "/mappings/unmatched"
    Then the response JSON at "$.mappings" has 1 items
    And the response JSON at "$.mappings[0].name" is "idle"

  Scenario: One journal entry can be read and deleted by id
    Given the global mapping:
      """json
      { "request": { "urlPath": "/a" }, "response": { "status": 200 } }
      """
    And I send a "GET" request to "/a"
    And I send a "GET" request to "/a"
    And I call admin "GET" "/requests"
    And I remember the response JSON at "$.requests[0].id" as "ENTRY"
    When I call admin "GET" "/requests/${ENTRY}"
    Then the response status is 200
    When I call admin "DELETE" "/requests/${ENTRY}"
    Then the response status is 200
    And admin "GET" "/requests/${ENTRY}" returns status 404
    And admin "GET" "/requests" returns 1 requests

  Scenario: The version endpoint reports the build
    When I call admin "GET" "/version"
    Then the response status is 200
    And the response JSON at "$.version" is "acceptance"

  Scenario: Live stats count served and unmatched requests
    Given the global mapping:
      """json
      { "request": { "urlPath": "/ok" }, "response": { "status": 200 } }
      """
    And I send a "GET" request to "/ok"
    And I send a "GET" request to "/ok"
    And I send a "GET" request to "/missing"
    When I call admin "GET" "/stats/live?window=5m"
    Then the response status is 200
    And the response JSON at "$.window" is "5m"
    And the response JSON at "$.unmatched" is 1
    And the response JSON at "$.topUnmatchedPaths[0].path" is "/missing"

  Scenario: Live stats refuse a window that is not offered
    When I call admin "GET" "/stats/live?window=2h"
    Then the response status is 422
    And the response JSON at "$.error" is "validation_failed"
