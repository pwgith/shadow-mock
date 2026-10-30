@saas @P5 @FR-9.4 @FR-9.5
Feature: Roles and permissions on the engine admin API
  Any org member with the editor role can create and manage any of the org's
  sessions. Viewers are read-only. Teams are never checked by the engine.

  Background:
    Given Shadow Mock is running in SaaS mode
    And the org "acme" with handle "acme" on plan "Team"
    And "bob" in org "acme" created a key-based shadow mock "S1"

  Scenario Outline: What each role can do with sessions
    Given I am "<user>" in org "acme" with role "<role>"
    When I call admin "<method>" "<path>"
    Then the response status is <status>

    Examples:
      | user  | role       | method | path                         | status |
      | alice | member     | GET    | /sessions                    | 200    |
      | alice | member     | POST   | /sessions/key-based          | 201    |
      | alice | member     | DELETE | /sessions/${S1.id}           | 200    |
      | alice | member     | POST   | /sessions/${S1.id}/mappings  | 201    |
      | carol | admin      | DELETE | /sessions/${S1.id}           | 200    |
      | vic   | viewer     | GET    | /sessions/${S1.id}/requests  | 200    |
      | vic   | viewer     | POST   | /sessions/key-based          | 403    |
      | vic   | viewer     | POST   | /sessions/${S1.id}/mappings  | 403    |

  Scenario: Sessions record who created them, including the label of a Tool app key
    Given I am the Tool app key "ci-tests" in org "acme"
    When I call admin "POST" "/sessions/key-based" with:
      """json
      {}
      """
    Then the created session has "createdBy" equal to "ci-tests"

  Scenario: Team membership has no effect on engine permissions
    Given "alice" is in no teams
    And I am "alice" in org "acme" with app role "member"
    When I call admin "POST" "/sessions/url-based" with:
      """json
      {}
      """
    Then the response status is 201
