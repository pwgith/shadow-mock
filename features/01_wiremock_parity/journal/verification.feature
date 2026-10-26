@wiremock @P0 @FR-5.2 @FR-5.5
Feature: Request verification
  Tests count and find requests by pattern after driving the system under
  test. Verification sees every request served before the response returned.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPathPattern": "/orders/.*" }, "response": { "status": 200 } }
      """
    And I send a "POST" request to "/orders/1" with body:
      """json
      { "sku": "A1" }
      """
    And I send a "POST" request to "/orders/2" with body:
      """json
      { "sku": "B2" }
      """
    And I send a "GET" request to "/orders/1"

  Scenario Outline: Count requests matching a pattern
    When I call admin "POST" "/requests/count" with:
      """json
      <pattern>
      """
    Then the response JSON at "$.count" is <count>

    Examples:
      | pattern                                                                                          | count |
      | { "method": "POST", "urlPathPattern": "/orders/.*" }                                             | 2     |
      | { "method": "ANY", "urlPath": "/orders/1" }                                                      | 2     |
      | { "method": "POST", "urlPathPattern": "/orders/.*", "bodyPatterns": [ { "matchesJsonPath": { "expression": "$.sku", "equalTo": "B2" } } ] } | 1 |
      | { "method": "DELETE", "urlPathPattern": "/orders/.*" }                                            | 0     |

  Scenario: Find requests returns the full entries
    When I call admin "POST" "/requests/find" with:
      """json
      { "method": "POST", "urlPath": "/orders/2" }
      """
    Then the response JSON at "$.requests" has 1 items
    And the response JSON at "$.requests[0].body" contains "B2"

  Scenario: Remove matching requests from the journal
    When I call admin "POST" "/requests/remove" with:
      """json
      { "method": "GET" }
      """
    Then admin "POST" "/requests/count" with pattern '{ "method": "ANY", "urlPathPattern": "/orders/.*" }' returns 2

  @multi-instance @saas
  Scenario: Verification sees requests served by any instance
    Given Shadow Mock is running with 2 instances
    And a key-based shadow mock "S1"
    When I send a "POST" request to "/orders/9" through instance 1 with header "X-Shadow-Mock-Key" set to "${S1.key}"
    And I call admin "POST" "/sessions/${S1.id}/requests/count" through instance 2 with:
      """json
      { "method": "POST", "urlPath": "/orders/9" }
      """
    Then the response JSON at "$.count" is 1
