@key-based @url-based @P2 @FR-7.13
Feature: A shadow mock can refuse to fall through to the global session
  By default a request the shadow mock does not cover falls through to the global session. A test that wants a missing
  mapping to fail can create the shadow mock with inheritGlobal set to false.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPath": "/customers/1" }, "response": { "status": 200, "body": "global-customer" } }
      """

  Scenario: Left out, inheritGlobal is true and requests fall through
    Given a key-based shadow mock "S1"
    When I send a "GET" request to "/customers/1" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response body is "global-customer"
    When I call admin "GET" "/sessions/${S1.id}"
    Then the response JSON at "$.inheritGlobal" is true

  Scenario: A key-based shadow mock without the fallback answers only from its own mappings
    Given a key-based shadow mock "S1" with:
      """json
      { "inheritGlobal": false }
      """
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/payments" }, "response": { "status": 402, "body": "declined" } }
      """
    When I send a "POST" request to "/payments" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 402
    When I send a "GET" request to "/customers/1" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 404
    And the response body does not contain "global-customer"

  Scenario: A URL-based shadow mock without the fallback answers only from its own mappings
    Given a URL-based shadow mock "U1" with:
      """json
      { "inheritGlobal": false }
      """
    When I send a "GET" request to "/customers/1" via "U1"
    Then the response status is 404
    And the response body does not contain "global-customer"

  Scenario: The shadow mock reports that it does not inherit
    Given a key-based shadow mock "S1" with:
      """json
      { "inheritGlobal": false }
      """
    When I call admin "GET" "/sessions/${S1.id}"
    Then the response JSON at "$.inheritGlobal" is false
    When I call admin "GET" "/sessions"
    Then the response JSON at "$.sessions[0].inheritGlobal" is false

  Scenario: Other tests and the global session are unaffected
    Given a key-based shadow mock "S1" with:
      """json
      { "inheritGlobal": false }
      """
    And a key-based shadow mock "S2"
    When I send a "GET" request to "/customers/1" with header "X-Shadow-Mock-Key" set to "${S2.key}"
    Then the response body is "global-customer"
    When I send a "GET" request to "/customers/1"
    Then the response body is "global-customer"

  Scenario: The 404 names the global mapping that was not inherited
    Given a key-based shadow mock "S1" with:
      """json
      { "inheritGlobal": false }
      """
    When I send a "GET" request to "/customers/1?x=1" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 404
    And the response body contains "not inherited by this shadow mock"

  Scenario: The near-miss endpoint marks the global mapping as skipped
    Given a key-based shadow mock "S1" with:
      """json
      { "inheritGlobal": false }
      """
    When I call admin "POST" "/sessions/${S1.id}/near-misses/request" with:
      """json
      { "method": "GET", "url": "/customers/2" }
      """
    Then the response JSON at "$.nearMisses[0].skipped" is true

  Scenario: A nested key-based shadow mock skips the global layer when its parent does
    Given a URL-based shadow mock "U1" with:
      """json
      { "inheritGlobal": false }
      """
    And a key-based shadow mock "S1" with:
      """json
      { "parentId": "${U1.id}" }
      """
    When I send a "GET" request to "/customers/1" via "U1" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 404

  Scenario: The setting cannot be changed afterwards
    Given a key-based shadow mock "S1" with:
      """json
      { "inheritGlobal": false }
      """
    When I call admin "PATCH" "/sessions/${S1.id}" with:
      """json
      { "inheritGlobal": true }
      """
    Then the response status is 422
