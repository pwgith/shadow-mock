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

  Scenario: A token added later selects the session from then on
    Given a key-based shadow mock "S1"
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/credit-check" }, "response": { "status": 200, "jsonBody": { "decision": "DECLINE" } } }
      """
    When I send a "GET" request to "/credit-check?customer=POL-555"
    Then the response JSON at "$.decision" is "APPROVE"
    When I call admin "PATCH" "/sessions/${S1.id}" with:
      """json
      { "addTokens": ["POL-555"] }
      """
    And I send a "GET" request to "/credit-check?customer=POL-555"
    Then the response JSON at "$.decision" is "DECLINE"

  Scenario: A removed token no longer selects the session
    Given a key-based shadow mock "S1" with:
      """json
      { "tokens": ["CUST-99812"] }
      """
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/credit-check" }, "response": { "status": 200, "jsonBody": { "decision": "DECLINE" } } }
      """
    When I send a "GET" request to "/credit-check?customer=CUST-99812"
    Then the response JSON at "$.decision" is "DECLINE"
    When I call admin "PATCH" "/sessions/${S1.id}" with:
      """json
      { "removeTokens": ["CUST-99812"] }
      """
    Then the response status is 200
    When I send a "GET" request to "/credit-check?customer=CUST-99812"
    Then the response JSON at "$.decision" is "APPROVE"

  Scenario: One patch can add and remove tokens
    Given a key-based shadow mock "S1" with:
      """json
      { "tokens": ["order-7731"] }
      """
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/credit-check" }, "response": { "status": 200, "jsonBody": { "decision": "DECLINE" } } }
      """
    When I call admin "PATCH" "/sessions/${S1.id}" with:
      """json
      { "addTokens": ["order-8842"], "removeTokens": ["order-7731"] }
      """
    Then the response JSON at "$.tokens" has 1 items
    And the response JSON at "$.tokens" contains "order-8842"
    When I send a "GET" request to "/credit-check?o=order-8842"
    Then the response JSON at "$.decision" is "DECLINE"
    When I send a "GET" request to "/credit-check?o=order-7731"
    Then the response JSON at "$.decision" is "APPROVE"

  Scenario: A removed token is free for another session to use
    Given a key-based shadow mock "S1" with:
      """json
      { "tokens": ["CUST-1"] }
      """
    And I call admin "PATCH" "/sessions/${S1.id}" with:
      """json
      { "removeTokens": ["CUST-1"] }
      """
    When I call admin "POST" "/sessions/key-based" with:
      """json
      { "tokens": ["CUST-1"] }
      """
    Then the response status is 201

  Scenario: A token works in the designated key header
    Given a key-based shadow mock "S1" with:
      """json
      { "tokens": ["CUST-99812"] }
      """
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/credit-check" }, "response": { "status": 200, "jsonBody": { "decision": "DECLINE" } } }
      """
    When I send a "GET" request to "/credit-check" with header "X-Shadow-Mock-Key" set to "CUST-99812"
    Then the response JSON at "$.decision" is "DECLINE"
    And the journal entry records detection stage "Header"

  Scenario: Patching in a token that another session uses is refused and changes nothing
    Given a key-based shadow mock "S1" with:
      """json
      { "tokens": ["CUST-1"] }
      """
    And a key-based shadow mock "S2"
    And the mapping in "S2":
      """json
      { "request": { "urlPath": "/credit-check" }, "response": { "status": 200, "jsonBody": { "decision": "DECLINE" } } }
      """
    When I call admin "PATCH" "/sessions/${S2.id}" with:
      """json
      { "addTokens": ["FREE-111", "CUST-1"] }
      """
    Then the response status is 409
    And the response JSON at "$.error" is "token_in_use"
    When I send a "GET" request to "/credit-check?customer=FREE-111"
    Then the response JSON at "$.decision" is "APPROVE"
    When I call admin "GET" "/sessions/${S2.id}"
    Then the response JSON at "$.tokens" is absent

  Scenario: Patching in a token that is too short is refused and changes nothing
    Given a key-based shadow mock "S1" with:
      """json
      { "tokens": ["CUST-99812"] }
      """
    When I call admin "PATCH" "/sessions/${S1.id}" with:
      """json
      { "addTokens": ["GOOD-TOKEN", "ab"], "removeTokens": ["CUST-99812"] }
      """
    Then the response status is 422
    And the response JSON at "$.error" is "token_too_short"
    When I call admin "GET" "/sessions/${S1.id}"
    Then the response JSON at "$.tokens" has 1 items
    And the response JSON at "$.tokens" contains "CUST-99812"

  Scenario: Adding a token the session already has changes nothing
    Given a key-based shadow mock "S1" with:
      """json
      { "tokens": ["CUST-99812"] }
      """
    When I call admin "PATCH" "/sessions/${S1.id}" with:
      """json
      { "addTokens": ["CUST-99812"] }
      """
    Then the response status is 200
    And the response JSON at "$.tokens" has 1 items

  Scenario: Only key-based shadow mocks have tokens
    Given a URL-based shadow mock "U1"
    When I call admin "PATCH" "/sessions/${U1.id}" with:
      """json
      { "addTokens": ["CUST-99812"] }
      """
    Then the response status is 422
    And the response JSON at "$.error" is "validation_failed"

  Scenario: Patching tokens on an unknown session is a 404
    When I call admin "PATCH" "/sessions/ses_00000000000000000000000000" with:
      """json
      { "addTokens": ["CUST-99812"] }
      """
    Then the response status is 404

  Scenario: A token stops working when its session expires
    Given the test clock is at "2026-11-01T09:00:00Z"
    And a key-based shadow mock "S1" with:
      """json
      { "ttlSeconds": 600, "tokens": ["CUST-99812"] }
      """
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/credit-check" }, "response": { "status": 200, "jsonBody": { "decision": "DECLINE" } } }
      """
    When the test clock advances by "11m"
    And I send a "GET" request to "/credit-check?customer=CUST-99812"
    Then the response JSON at "$.decision" is "APPROVE"

  Scenario: Tokens are listed on the session but the key is not
    Given a key-based shadow mock "S1" with:
      """json
      { "tokens": ["CUST-99812"] }
      """
    When I call admin "GET" "/sessions/${S1.id}"
    Then the response JSON at "$.tokens" contains "CUST-99812"
    And the response JSON at "$.key" is absent

  Scenario: The session key cannot be chosen by the caller
    When I call admin "POST" "/sessions/key-based" with:
      """json
      { "key": "smk_CHOSENBYCALLER000000000000" }
      """
    Then the response JSON at "$.key" matches "^smk_[0-9A-Z]{26}$"
    And the response JSON at "$.key" does not match "^smk_CHOSENBYCALLER"

  @saas
  Scenario: The same token may be used by sessions in different orgs
    Given the org "acme" has a key-based shadow mock with token "CUST-1"
    When org "globex" creates a key-based shadow mock with token "CUST-1"
    Then the response status is 201
