@managed @P5 @FR-9.5a @FR-6.5 @TA-3
Feature: Global session writes only through the management app
  In SaaS mode, the engine rejects global-scope writes from anyone, including
  org admins. The management app makes them, after checking team grants.
  Session-scoped equivalents are allowed.

  Background:
    Given Shadow Mock is running in SaaS mode
    And the org "acme" with handle "acme" on plan "Team"
    And I am "carol" in org "acme" with app role "admin"
    And a key-based shadow mock "S1"

  Scenario Outline: Global-scope writes are rejected
    When I call admin "<method>" "<path>" with:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    Then the response status is 403
    And the response JSON at "$.error" is "global_writes_via_management_app"

    Examples:
      | method | path                                         |
      | POST   | /mappings                                    |
      | PUT    | /mappings/aaaaaaaa-0000-0000-0000-000000000001 |
      | DELETE | /mappings                                    |
      | POST   | /mappings/import                             |
      | POST   | /mappings/reset                              |
      | PUT    | /files/x.json                                |
      | POST   | /settings                                    |
      | POST   | /reset                                       |
      | POST   | /recordings/start                            |

  Scenario: Global reads are allowed
    When I call admin "GET" "/mappings"
    Then the response status is 200

  Scenario: The same write scoped to a session is allowed
    When I call admin "POST" "/mappings" with header "X-Shadow-Mock-Key" set to "${S1.key}" and body:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    Then the response status is 201

  @management-app
  Scenario: A global mapping written by the management app is served by the engine
    Given "dana" is in team "platform-qa" which has grant "global:write"
    When "dana" creates the global mapping in the management app:
      """json
      { "request": { "urlPath": "/from-ui" }, "response": { "status": 200, "body": "ui" } }
      """
    Then within 1 s a "GET" request to "/acme/from-ui" returns body "ui"
