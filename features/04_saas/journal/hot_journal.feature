@saas @P5 @FR-5.5 @NFR-4.4
Feature: Hot journal in the shared cache
  Every served request goes into a capped Redis list for its session (and for
  the org's recent global traffic) with full bodies. Verification and near
  misses read from it.

  Background:
    Given Shadow Mock is running in SaaS mode
    And the org "acme" with handle "acme" on plan "Team"
    And org "acme" has the global mapping:
      """json
      { "request": { "urlPathPattern": "/.*" }, "response": { "status": 200 } }
      """

  Scenario Outline: Hot journal caps follow the plan
    Given the org "acme" is on Shadow Mock plan "<plan>"
    And a key-based shadow mock "S1"
    When I send <sent> requests with key "${S1.key}"
    Then admin "GET" "/sessions/${S1.id}/requests" lists <kept> requests
    And the oldest requests were dropped first

    Examples:
      | plan     | sent | kept |
      | Free     | 250  | 200  |
      | Team     | 1200 | 1000 |
      | Business | 5100 | 5000 |

  Scenario: Large bodies are truncated in the hot copy but kept in full in history
    Given a key-based shadow mock "S1"
    When I send a "POST" request to "/big" with a 200 KB body and key "${S1.key}"
    Then the hot journal entry has a 64 KB body flagged "truncated"
    And within 10 s the warm journal entry has the full 200 KB body

  Scenario: Verification still matches on body patterns within the hot copy
    Given a key-based shadow mock "S1"
    When I send a "POST" request to "/orders" with key "${S1.key}" and body:
      """json
      { "sku": "A1" }
      """
    And I call admin "POST" "/sessions/${S1.id}/requests/count" with:
      """json
      { "method": "POST", "urlPath": "/orders", "bodyPatterns": [ { "matchesJsonPath": { "expression": "$.sku", "equalTo": "A1" } } ] }
      """
    Then the response JSON at "$.count" is 1

  Scenario: The session's hot journal expires with the session
    Given a key-based shadow mock "S1" with:
      """json
      { "ttlSeconds": 60 }
      """
    And I send 3 requests with key "${S1.key}"
    When the test clock advances by "2m"
    Then Redis holds no hot journal for "S1"

  Scenario: The org's recent-traffic list expires after an hour idle
    Given I send 3 requests with no session key
    When the test clock advances by "61m"
    Then Redis holds no recent-traffic list for org "acme"
