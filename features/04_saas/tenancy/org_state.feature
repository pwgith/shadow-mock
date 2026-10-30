@saas @P5 @FR-11.5 @FR-9.7 @security @TA-14
Feature: Suspended and deleted orgs
  An org's plan, limits and status are ordinary configuration rows. A suspended org is refused but kept; a deleted
  org is a tombstone that nothing recreates.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"
    And a mapping "hello" in the global layer for "GET /hello"
    And a Tool app key "ci" for org "acme" with scopes "sessions,read"

  Scenario: A suspended org's mock traffic is rejected with a reason
    When the management app sets the status of org "acme" to "suspended"
    And the SUT sends "GET /acme/hello"
    Then the response status is 403
    And the response JSON at "$.error" is "org_suspended"
    And the response header "X-Shadow-Mock-Reason" is "org_suspended"

  Scenario: A suspended org's keys and tokens are refused
    Given the status of org "acme" is "suspended"
    When I call admin "GET" "/sessions" with the key "ci"
    Then the response status is 403
    When I call admin "GET" "/sessions" with a token for "alice" in org "acme"
    Then the response status is 403

  Scenario: Serving resumes within a second of the status changing back
    Given the status of org "acme" is "suspended"
    When the management app sets the status of org "acme" to "active"
    Then within 1 second "GET /acme/hello" is served

  Scenario: Configuration survives a suspension
    Given the status of org "acme" is "suspended"
    When the management app sets the status of org "acme" to "active"
    Then the mapping "hello" is still there

  Scenario: Other orgs are not affected
    Given the org "globex" with handle "globex" on plan "Team"
    When the management app sets the status of org "acme" to "suspended"
    Then "GET /globex/hello" is served

  Scenario Outline: A deleted org stays deleted
    Given the management app marks org "acme" as deleted
    When <action>
    Then the response status is 410
    And the response JSON at "$.error" is "org_deleted"

    Examples:
      | action                                                                |
      | the SUT sends "GET /acme/hello"                                        |
      | I call admin "GET" "/sessions" with the key "ci"                       |
      | I call admin "GET" "/sessions" with a token for "alice" in org "acme"  |
      | I call admin "POST" "/sessions/key-based" with a token for "alice" in org "acme" |

  Scenario: No runtime state is created for a deleted org
    Given the management app marks org "acme" as deleted
    When the SUT sends "GET /acme/hello"
    Then no session, counter, journal row or stats row exists for org "acme"

  Scenario: A cached key stops within a minute of the org being deleted
    Given the key "ci" has been used
    When the management app marks org "acme" as deleted
    And 61 seconds pass
    Then the key "ci" is refused with 410

  Scenario: A deleted org is not recreated by a late configuration write
    Given the management app marks org "acme" as deleted
    When a late write sets the status of org "acme" to "active"
    Then org "acme" is still deleted

  Scenario: A deleted org's handle and aliases stay reserved
    Given the management app marks org "acme" as deleted
    When the management app creates org "other" with handle "acme"
    Then the write is refused as "handle_reserved"

  Scenario: Past the hard cap and its grace the reason is stated
    Given the plan's monthly request cap is 1000 with a grace of 100
    And the org has served 1100 requests this month
    When the SUT sends "GET /acme/hello"
    Then the response status is 429
    And the response JSON at "$.error" is "monthly_request_cap"
    And the response header "X-Shadow-Mock-Reason" is "monthly_request_cap"

  Scenario: A paid plan keeps serving past its included requests
    Given the org is on a plan with a soft cap
    And the org has served more requests than the plan includes
    When the SUT sends "GET /acme/hello"
    Then the response status is 200
