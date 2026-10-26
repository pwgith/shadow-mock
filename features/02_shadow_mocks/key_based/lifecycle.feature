@key-based @P1 @FR-7.1 @FR-7.2 @NFR-5.1
Feature: Key-based shadow mock lifecycle
  A test creates its own key-based shadow mock, gets a unique session key,
  and the session expires on its own if the test forgets to delete it.

  Background:
    Given Shadow Mock is running
    And the test clock is at "2026-11-01T09:00:00Z"

  Scenario: Create a key-based shadow mock
    When I call admin "POST" "/sessions/key-based" with:
      """json
      { "name": "checkout-declined-card" }
      """
    Then the response status is 201
    And the response JSON at "$.id" matches "^ses_[0-9A-Z]{26}$"
    And the response JSON at "$.key" matches "^smk_[0-9A-Z]{26}$"
    And the response JSON at "$.expiresAt" is "2026-11-01T10:00:00Z"

  Scenario: Every session key is unique
    When I create 1000 key-based shadow mocks
    Then all 1000 session keys are distinct

  Scenario Outline: TTL defaults and limits
    When I call admin "POST" "/sessions/key-based" with:
      """json
      { "ttlSeconds": <ttl> }
      """
    Then the response status is <status>
    And the response JSON at "<path>" is "<value>"

    Examples:
      | ttl    | status | path       | value                      |
      | 600    | 201    | $.expiresAt | 2026-11-01T09:10:00Z      |
      | 86400  | 201    | $.expiresAt | 2026-11-02T09:00:00Z      |
      | 86401  | 422    | $.error     | ttl_exceeds_maximum       |
      | 0      | 422    | $.error     | ttl_must_be_positive      |

  Scenario: A session expires after its TTL and its mocks stop applying
    Given the global mapping:
      """json
      { "request": { "urlPath": "/pay" }, "response": { "status": 200, "body": "global" } }
      """
    And a key-based shadow mock "S1" with:
      """json
      { "ttlSeconds": 600 }
      """
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/pay" }, "response": { "status": 402, "body": "declined" } }
      """
    When the test clock advances by "11m"
    And I send a "GET" request to "/pay" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response body is "global"
    And admin "GET" "/sessions/${S1.id}" returns status 404

  Scenario: Keepalive extends the TTL by its original duration
    Given a key-based shadow mock "S1" with:
      """json
      { "ttlSeconds": 600 }
      """
    And the test clock advances by "5m"
    When I call admin "POST" "/sessions/${S1.id}/keepalive"
    Then the response JSON at "$.expiresAt" is "2026-11-01T09:15:00Z"

  Scenario: PATCH can extend the TTL up to the maximum
    Given a key-based shadow mock "S1"
    When I call admin "PATCH" "/sessions/${S1.id}" with:
      """json
      { "ttlSeconds": 7200 }
      """
    Then the response JSON at "$.expiresAt" is "2026-11-01T11:00:00Z"

  Scenario: Delete a session explicitly
    Given a key-based shadow mock "S1"
    When I call admin "DELETE" "/sessions/${S1.id}"
    Then the response status is 200
    And admin "GET" "/sessions/${S1.id}" returns status 404

  Scenario: Expiry discards the session's mappings, journal and call counters
    Given a key-based shadow mock "S1" with:
      """json
      { "ttlSeconds": 60 }
      """
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    And I send a "GET" request to "/x" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    When the test clock advances by "2m"
    Then the store holds no mappings, journal entries or call counters for "S1"

  Scenario: List active sessions with a kind filter
    Given a key-based shadow mock "S1"
    And a URL-based shadow mock "U1"
    When I call admin "GET" "/sessions?kind=key-based"
    Then the response JSON at "$.sessions" has 1 items
    And the response JSON at "$.sessions[0].id" is "${S1.id}"

  Scenario: The raw session key is only returned at creation
    Given a key-based shadow mock "S1"
    When I call admin "GET" "/sessions/${S1.id}"
    Then the response JSON at "$.key" is absent
    And the response JSON at "$.keyPrefix" is the first 8 characters of "${S1.key}"
