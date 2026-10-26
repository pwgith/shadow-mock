@key-based @P1 @FR-7.7 @FR-7.8
Feature: Override and mask global mappings
  A session mapping with the same id as a global mapping replaces it for that
  session, even when the session copy does not match. A mask hides a global
  mapping without replacing it.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "id": "cccccccc-0000-0000-0000-000000000001", "request": { "urlPath": "/rates" }, "response": { "status": 200, "body": "global-rates" } }
      """
    And the global mapping:
      """json
      { "priority": 9, "request": { "urlPathPattern": "/rates.*" }, "response": { "status": 200, "body": "catch-all" } }
      """
    And a key-based shadow mock "S1"

  Scenario: Override by id replaces the global mapping for this session only
    Given the mapping in "S1":
      """json
      { "id": "cccccccc-0000-0000-0000-000000000001", "request": { "urlPath": "/rates" }, "response": { "status": 200, "body": "session-rates" } }
      """
    When I send a "GET" request to "/rates" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response body is "session-rates"
    When I send a "GET" request to "/rates"
    Then the response body is "global-rates"

  Scenario: An overriding mapping that does not match still hides the global one
    Given the mapping in "S1":
      """json
      { "id": "cccccccc-0000-0000-0000-000000000001", "request": { "urlPath": "/rates", "headers": { "X-Never": { "equalTo": "sent" } } },
        "response": { "status": 200, "body": "never" } }
      """
    When I send a "GET" request to "/rates" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response body is "catch-all"

  Scenario: Mask by id
    When I call admin "PATCH" "/sessions/${S1.id}" with:
      """json
      { "addMasks": [ { "id": "cccccccc-0000-0000-0000-000000000001" } ] }
      """
    And I send a "GET" request to "/rates" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response body is "catch-all"

  Scenario: Mask by request pattern
    When I call admin "PATCH" "/sessions/${S1.id}" with:
      """json
      { "addMasks": [ { "request": { "urlPathPattern": "/rates.*" } } ] }
      """
    And I send a "GET" request to "/rates" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 404

  Scenario: Masks can be supplied when the session is created
    Given a key-based shadow mock "S2" with:
      """json
      { "masks": [ { "id": "cccccccc-0000-0000-0000-000000000001" } ] }
      """
    When I send a "GET" request to "/rates" with header "X-Shadow-Mock-Key" set to "${S2.key}"
    Then the response body is "catch-all"

  Scenario: Session mappings never become underlays
    Given the mapping in "S1":
      """json
      { "priority": 1, "request": { "urlPath": "/rates" }, "response": { "status": 200, "body": "session-first" } }
      """
    When I send a "GET" request to "/rates" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response body is "session-first"
