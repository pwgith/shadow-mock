@key-based @P1 @FR-7.9
Feature: Match tokens
  A session can register extra strings, such as a test customer number, that
  select it when the system under test forwards test data but not the key.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPath": "/credit-check" }, "response": { "status": 200, "jsonBody": { "decision": "APPROVE" } } }
      """

  Scenario: A token in the body selects the session
    Given a key-based shadow mock "S1" with:
      """json
      { "tokens": ["CUST-99812"] }
      """
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/credit-check" }, "response": { "status": 200, "jsonBody": { "decision": "DECLINE" } } }
      """
    When I send a "POST" request to "/credit-check" with body:
      """json
      { "customerId": "CUST-99812", "amount": 500 }
      """
    Then the response JSON at "$.decision" is "DECLINE"
    When I send a "POST" request to "/credit-check" with body:
      """json
      { "customerId": "CUST-11111", "amount": 500 }
      """
    Then the response JSON at "$.decision" is "APPROVE"

  Scenario: Tokens are also found in metadata
    Given a key-based shadow mock "S1" with:
      """json
      { "tokens": ["order-7731"] }
      """
    And the mapping in "S1":
      """json
      { "request": { "urlPathPattern": "/orders/.*" }, "response": { "status": 200, "body": "shadow" } }
      """
    When I send a "GET" request to "/orders/order-7731"
    Then the response body is "shadow"

  Scenario: Tokens can be added later with PATCH
    Given a key-based shadow mock "S1"
    When I call admin "PATCH" "/sessions/${S1.id}" with:
      """json
      { "addTokens": ["POL-555"] }
      """
    Then the response JSON at "$.tokens" contains "POL-555"

  Scenario: A token already used by another active session is rejected
    Given a key-based shadow mock "S1" with:
      """json
      { "tokens": ["CUST-1"] }
      """
    When I call admin "POST" "/sessions/key-based" with:
      """json
      { "tokens": ["CUST-1"] }
      """
    Then the response status is 409
    And the response JSON at "$.error" is "token_in_use"

  Scenario: A token is free again once its session has gone
    Given a key-based shadow mock "S1" with:
      """json
      { "tokens": ["CUST-1"] }
      """
    And I call admin "DELETE" "/sessions/${S1.id}"
    When I call admin "POST" "/sessions/key-based" with:
      """json
      { "tokens": ["CUST-1"] }
      """
    Then the response status is 201

  Scenario Outline: Tokens shorter than the minimum length are rejected
    When I call admin "POST" "/sessions/key-based" with:
      """json
      { "tokens": ["<token>"] }
      """
    Then the response status is <status>

    Examples:
      | token  | status |
      | AB12   | 422    |
      | ABC123 | 201    |

  @saas
  Scenario: The same token may be used by sessions in different orgs
    Given the org "acme" has a key-based shadow mock with token "CUST-1"
    When org "globex" creates a key-based shadow mock with token "CUST-1"
    Then the response status is 201
