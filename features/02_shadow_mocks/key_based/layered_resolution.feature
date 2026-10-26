@key-based @P1 @FR-7.5 @FR-7.12
Feature: Layered resolution: the session first, then the global session
  The detected session's mappings are tried first. If none match, the request
  falls through to the global session. The layer always wins over priority.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "priority": 1, "request": { "urlPath": "/payments" }, "response": { "status": 200, "body": "global-payments" } }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/customers/1" }, "response": { "status": 200, "body": "global-customer" } }
      """
    And a key-based shadow mock "S1"
    And the mapping in "S1":
      """json
      { "priority": 10, "request": { "urlPath": "/payments" }, "response": { "status": 402, "body": "declined" } }
      """

  Scenario: A matching session mapping wins even with a worse priority
    When I send a "POST" request to "/payments" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 402
    And the response body is "declined"

  Scenario: Requests the session does not mock fall through to the global session
    When I send a "GET" request to "/customers/1" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response body is "global-customer"

  Scenario: Other tests on the same environment are unaffected
    Given a key-based shadow mock "S2"
    When I send a "POST" request to "/payments" with header "X-Shadow-Mock-Key" set to "${S2.key}"
    Then the response body is "global-payments"
    When I send a "POST" request to "/payments"
    Then the response body is "global-payments"

  Scenario: A session mapping that does not match falls through even for the same URL
    Given the mapping in "S1":
      """json
      { "priority": 1, "request": { "urlPath": "/customers/1", "headers": { "X-Variant": { "equalTo": "vip" } } }, "response": { "status": 200, "body": "vip" } }
      """
    When I send a "GET" request to "/customers/1" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response body is "global-customer"

  Scenario: No match in any layer gives a 404 with near misses from every layer
    When I send a "GET" request to "/nowhere" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 404
    And the response body lists near misses from layers "key-based" and "global"

  Scenario Outline: The optional matched-layer response header
    Given Shadow Mock is running with configuration:
      """yaml
      respond: { matchedHeader: <enabled> }
      """
    And a key-based shadow mock "S1"
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/payments" }, "response": { "status": 402 } }
      """
    When I send a "POST" request to "/payments" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response header "X-Shadow-Mock-Matched" is "<header>"

    Examples:
      | enabled | header                      |
      | true    | ${S1.id};layer=key-based    |
      | false   | absent                      |
