@saas @P5 @FR-9.6a @security
Feature: The JWT org must match the org in the path
  The JWT is the source of truth for identity, and the path must agree with
  it. A token for one org can never act on another.

  Background:
    Given Shadow Mock is running in SaaS mode
    And the org "acme" with handle "acme" on plan "Team"
    And the org "globex" with handle "globex" on plan "Team"

  Scenario: Matching org is allowed
    Given I am "alice" in org "acme" with role "org:member"
    When I call admin "POST" "https://shadowmock.test/acme/__admin/sessions/key-based" with:
      """json
      {}
      """
    Then the response status is 201

  Scenario: A user in several orgs with the wrong active org gets a clear error
    Given "alice" is a member of orgs "acme" and "globex"
    And I am "alice" with active org "globex"
    When I call admin "POST" "https://shadowmock.test/acme/__admin/sessions/key-based" with:
      """json
      {}
      """
    Then the response status is 403
    And the response JSON at "$.error" is "org_mismatch"
    And the response JSON at "$.message" contains "switch active org"
    And no session was created in either org

  Scenario: CI tokens are checked the same way
    Given I am CI key "ck_ACME0000000000000" in org "acme"
    When I call admin "GET" "https://shadowmock.test/globex/__admin/sessions"
    Then the response status is 403
    And the response JSON at "$.error" is "org_mismatch"

  Scenario: The check cannot be skipped by any admin endpoint
    Given I am "alice" in org "globex" with role "org:admin"
    When I call every admin endpoint under "https://shadowmock.test/acme/__admin"
    Then every response status is 403 or 404
