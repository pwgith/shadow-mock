@saas @P5 @FR-9.6 @FR-9.7
Feature: Org handle routing
  Every SaaS engine URL starts with the org handle. The engine strips the
  segment before matching, and never routes one org's traffic to another.

  Background:
    Given Shadow Mock is running in SaaS mode
    And the org "acme" with handle "acme" on plan "Team"
    And the org "globex" with handle "globex" on plan "Team"
    And org "acme" has the global mapping:
      """json
      { "request": { "urlPath": "/claims/1" }, "response": { "status": 200, "body": "acme-claim" } }
      """
    And org "globex" has the global mapping:
      """json
      { "request": { "urlPath": "/claims/1" }, "response": { "status": 200, "body": "globex-claim" } }
      """

  Scenario Outline: Each org's traffic reaches only its own global session
    When I send a "GET" request to "https://shadowmock.test/<handle>/claims/1"
    Then the response body is "<body>"

    Examples:
      | handle | body         |
      | acme   | acme-claim   |
      | globex | globex-claim |

  Scenario: An unknown handle returns 404 without revealing other orgs
    When I send a "GET" request to "https://shadowmock.test/initech/claims/1"
    Then the response status is 404
    And the response JSON at "$.error" is "unknown_org"

  Scenario: A request without an org segment is rejected in SaaS mode
    When I send a "GET" request to "https://shadowmock.test/claims/1"
    Then the response status is 404

  Scenario: URL-based shadow mocks live under their org's handle
    Given org "acme" has a URL-based shadow mock "U1"
    When I send a "GET" request to "https://shadowmock.test/acme/d/${U1.id}/claims/1"
    Then the response status is 200
    When I send a "GET" request to "https://shadowmock.test/globex/d/${U1.id}/claims/1"
    Then the response status is 404

  Scenario: A session key from another org is ignored
    Given org "globex" has a key-based shadow mock "G1" with the mapping:
      """json
      { "request": { "urlPath": "/claims/1" }, "response": { "status": 200, "body": "globex-shadow" } }
      """
    When I send a "GET" request to "https://shadowmock.test/acme/claims/1" with header "X-Shadow-Mock-Key" set to "${G1.key}"
    Then the response body is "acme-claim"

  Scenario: Platform health endpoints sit outside any org
    When I send a "GET" request to "https://shadowmock.test/__health"
    Then the response status is 200

  Scenario Outline: Reserved words cannot be handles
    When an org admin tries to set the handle to "<handle>"
    Then the change is rejected with "<reason>"

    Examples:
      | handle    | reason            |
      | __admin   | reserved          |
      | d         | reserved          |
      | api       | reserved          |
      | 9lives    | must start with a letter |
      | ab        | 3 to 40 characters |
      | Acme      | lowercase only    |
