@url-based @P2 @FR-8.1 @FR-7.2 @NFR-5.1
Feature: URL-based shadow mock lifecycle
  A CI pipeline creates a URL-based shadow mock and points the API under test
  at its base URL for all outbound calls.

  Background:
    Given Shadow Mock is running
    And the test clock is at "2026-11-01T09:00:00Z"

  Scenario: Create a URL-based shadow mock
    When I call admin "POST" "/sessions/url-based" with:
      """json
      { "name": "claims-api-ci" }
      """
    Then the response status is 201
    And the response JSON at "$.id" matches "^[a-z2-7]{16}$"
    And the response JSON at "$.baseUrl" is "{mock base}/d/" followed by the id
    And the response JSON at "$.expiresAt" is "2026-11-02T09:00:00Z"
    And the response JSON at "$.key" is absent

  Scenario Outline: URL-based TTL defaults and limits
    When I call admin "POST" "/sessions/url-based" with:
      """json
      { "ttlSeconds": <ttl> }
      """
    Then the response status is <status>

    Examples:
      | ttl     | status |
      | 3600    | 201    |
      | 2592000 | 201    |
      | 2592001 | 422    |

  @saas
  Scenario: The Free tier caps URL-based TTLs at 7 days
    Given the org "acme" is on Shadow Mock plan "Free"
    When I call admin "POST" "/sessions/url-based" with:
      """json
      { "ttlSeconds": 1209600 }
      """
    Then the response status is 422
    And the response JSON at "$.error" is "ttl_exceeds_plan_maximum"

  Scenario: URL-based IDs are unguessable and unique
    When I create 1000 URL-based shadow mocks
    Then all 1000 ids are distinct
    And every id has at least 80 bits of randomness

  Scenario: Keepalive and delete work as for key-based shadow mocks
    Given a URL-based shadow mock "U1" with:
      """json
      { "ttlSeconds": 86400 }
      """
    And the test clock advances by "12h"
    When I call admin "POST" "/sessions/${U1.id}/keepalive"
    Then the response JSON at "$.expiresAt" is "2026-11-02T21:00:00Z"
    When I call admin "DELETE" "/sessions/${U1.id}"
    Then a "GET" request to "/anything" via "U1" returns status 404
