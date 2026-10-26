@saas @P5 @FR-9.4 @FR-9.7 @security
Feature: Tenant isolation
  No org can read, change or influence another org's mappings, sessions,
  journals, call counters, files or stats.

  Background:
    Given Shadow Mock is running in SaaS mode
    And the org "acme" with handle "acme" on plan "Team"
    And the org "globex" with handle "globex" on plan "Team"
    And org "globex" has a key-based shadow mock "G1"
    And I am "alice" in org "acme" with role "org:admin"

  Scenario Outline: Cross-org admin access is impossible even with a valid id
    When I call admin "<method>" "https://shadowmock.test/acme/__admin<path>"
    Then the response status is 404

    Examples:
      | method | path                          |
      | GET    | /sessions/${G1.id}            |
      | GET    | /sessions/${G1.id}/mappings   |
      | GET    | /sessions/${G1.id}/requests   |
      | DELETE | /sessions/${G1.id}            |
      | POST   | /sessions/${G1.id}/keepalive  |

  Scenario: Session lists only show the caller's org
    When I call admin "GET" "https://shadowmock.test/acme/__admin/sessions"
    Then no session in the response belongs to org "globex"

  Scenario: Global journals are per org
    Given a request was sent to "https://shadowmock.test/globex/secret"
    When I call admin "GET" "https://shadowmock.test/acme/__admin/requests"
    Then no journal entry mentions "/secret"

  Scenario: Call counters are per org
    Given orgs "acme" and "globex" each have a global mapping with id "ffffffff-0000-0000-0000-000000000009" for "/n" that returns 201 on call 1
    And org "globex" has called "/n" 5 times
    When I send a "GET" request to "https://shadowmock.test/acme/n"
    Then the response status is 201

  Scenario: Shared journal body objects are unreachable except through the owner's rows
    Given org "globex" has a journal entry with a 10 KB body stored in a shared body object
    When org "acme" tries to read that body by guessing its object key and range
    Then no API exposes the object
