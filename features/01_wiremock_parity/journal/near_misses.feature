@wiremock @P3 @FR-5.3
Feature: Near misses
  For an unmatched request, list the closest mappings, ranked by distance.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "name": "close", "request": { "method": "GET", "urlPath": "/claims/1", "headers": { "X-Tenant": { "equalTo": "acme" } } }, "response": { "status": 200 } }
      """
    And the global mapping:
      """json
      { "name": "far", "request": { "method": "POST", "urlPath": "/payments" }, "response": { "status": 200 } }
      """

  @p3-harness
  Scenario: Near misses for a request
    When I call admin "POST" "/near-misses/request" with:
      """json
      { "method": "GET", "url": "/claims/1", "headers": { "X-Tenant": "other" } }
      """
    Then the response JSON at "$.nearMisses[0].stubMapping.name" is "close"
    And the response JSON at "$.nearMisses[0].matchResult.distance" is less than the distance for "far"

  Scenario: Near misses for every unmatched request in the journal
    Given I send a "GET" request to "/claims/1"
    When I call admin "GET" "/requests/unmatched/near-misses"
    Then the response JSON at "$.nearMisses[0].stubMapping.name" is "close"

  Scenario: Near misses for a request pattern against the journal
    Given I send a "GET" request to "/claims/2"
    When I call admin "POST" "/near-misses/request-pattern" with:
      """json
      { "method": "GET", "urlPath": "/claims/1" }
      """
    Then the response JSON at "$.nearMisses[0].request.url" is "/claims/2"
