@url-based @P2 @FR-8.3 @FR-8.4
Feature: URL-based resolution, overrides, isolation and scoped admin
  The URL-based shadow mock is tried first, then the global session. The same
  precedence, override, mask and scenario-isolation rules apply as for
  key-based shadow mocks.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "id": "dddddddd-0000-0000-0000-000000000001", "priority": 1, "request": { "urlPath": "/fx" }, "response": { "status": 200, "body": "global-fx" } }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/customers/1" }, "response": { "status": 200, "body": "global-customer" } }
      """
    And a URL-based shadow mock "U1"

  Scenario: The layer wins over priority, and unmatched requests fall through
    Given the mapping in "U1":
      """json
      { "priority": 10, "request": { "urlPath": "/fx" }, "response": { "status": 200, "body": "u1-fx" } }
      """
    When I send a "GET" request to "/fx" via "U1"
    Then the response body is "u1-fx"
    When I send a "GET" request to "/customers/1" via "U1"
    Then the response body is "global-customer"

  Scenario: Override by id and mask
    Given the mapping in "U1":
      """json
      { "id": "dddddddd-0000-0000-0000-000000000001", "request": { "urlPath": "/fx", "headers": { "X-No": { "equalTo": "match" } } }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/fx" via "U1"
    Then the response status is 404
    When I call admin "PATCH" "/sessions/${U1.id}" with:
      """json
      { "addMasks": [ { "request": { "urlPath": "/customers/1" } } ] }
      """
    And I send a "GET" request to "/customers/1" via "U1"
    Then the response status is 404

  Scenario: Scenario state is forked at creation, as for key-based shadow mocks
    Given the global mapping:
      """json
      { "scenarioName": "s", "requiredScenarioState": "Started", "newScenarioState": "Done",
        "request": { "method": "POST", "urlPath": "/step" }, "response": { "status": 200 } }
      """
    And the global mapping:
      """json
      { "scenarioName": "s", "requiredScenarioState": "Done", "request": { "urlPath": "/state" }, "response": { "status": 200, "body": "done" } }
      """
    When I send a "POST" request to "/step" via "U1"
    Then a "GET" request to "/state" via "U1" returns body "done"
    And a "GET" request to "/state" returns status 404

  Scenario: Session-scoped admin works for URL-based shadow mocks
    When I call admin "POST" "/sessions/${U1.id}/mappings" with:
      """json
      { "request": { "urlPath": "/new" }, "response": { "status": 201 } }
      """
    Then a "GET" request to "/new" via "U1" returns status 201
    And a "GET" request to "/new" returns status 404
    When I send a "GET" request to "/new" via "U1"
    And I call admin "POST" "/sessions/${U1.id}/requests/count" with:
      """json
      { "method": "GET", "urlPath": "/new" }
      """
    Then the response JSON at "$.count" is 2

  Scenario: Journal entries for URL-based traffic record the layer
    When I send a "GET" request to "/customers/1" via "U1"
    Then the latest journal entry has "shadowMock.layer" equal to "global" and "shadowMock.sessionId" equal to "${U1.id}"
