@standalone @P0 @FR-9.1 @FR-9.2 @FR-8.2
Feature: Standalone mode
  A single binary that drops in for WireMock standalone. There is no
  authentication: every admin call runs as user "local" in org "local" with
  the admin role, but the authentication and authorisation chain still runs.

  Background:
    Given Shadow Mock is running in standalone mode

  Scenario: Admin calls need no credentials
    Given I am not authenticated
    When I call admin "GET" "/mappings"
    Then the response status is 200

  Scenario: Sessions record the local principal
    When I call admin "POST" "/sessions/key-based" with:
      """json
      {}
      """
    Then the created session has "createdBy" equal to "local" and tenant "local"

  # Needs the audit log (P4).
  @wip
  Scenario: The authorisation chain runs even though it always passes
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    Then the audit log records action "mappings:write" authorised for principal "local" in org "local"

  Scenario Outline: The org segment is optional, and if present must be "local"
    Given a URL-based shadow mock "U1"
    And the global mapping:
      """json
      { "request": { "urlPath": "/claims/1" }, "response": { "status": 200, "body": "ok" } }
      """
    When I send a "GET" request to "<url>"
    Then the response status is <status>

    Examples:
      | url                                         | status |
      | http://localhost:8080/claims/1              | 200    |
      | http://localhost:8080/local/claims/1        | 200    |
      | http://localhost:8080/acme/claims/1         | 404    |
      | http://localhost:8080/d/${U1.id}/claims/1   | 200    |
      | http://localhost:8080/local/d/${U1.id}/claims/1 | 200 |

  Scenario: Global writes are allowed in standalone mode
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/allowed" }, "response": { "status": 200 } }
      """
    Then the response status is 201

  Scenario: A WireMock-style health endpoint is also served
    When I send a "GET" request to "/__admin/health"
    Then the response status is 200
