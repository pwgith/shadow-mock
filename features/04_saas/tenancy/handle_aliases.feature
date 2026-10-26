@saas @P5 @FR-9.6
Feature: Immutable handles and aliases
  The handle is Shadow Mock's own identifier for the org. It is unaffected by
  Clerk slug renames. A changed handle keeps the old one as a permanent alias.

  Background:
    Given Shadow Mock is running in SaaS mode
    And the Clerk org "org_123" with slug "acme" enables Shadow Mock
    And org "org_123" has the global mapping:
      """json
      { "request": { "urlPath": "/ping" }, "response": { "status": 200 } }
      """

  Scenario: The handle defaults to the Clerk slug at onboarding
    Then the org "org_123" has handle "acme"

  Scenario: Renaming the org in Clerk does not change the handle
    When the Clerk org "org_123" is renamed to slug "acme-corp"
    Then a "GET" request to "https://shadowmock.test/acme/ping" returns status 200
    And a "GET" request to "https://shadowmock.test/acme-corp/ping" returns status 404

  Scenario: Changing the handle keeps the old one as an alias
    When the org admin changes the handle of "org_123" to "acme-insurance"
    Then within 1 s a "GET" request to "https://shadowmock.test/acme-insurance/ping" returns status 200
    And a "GET" request to "https://shadowmock.test/acme/ping" returns status 200

  Scenario: An alias cannot be claimed by another org
    Given the org admin changed the handle of "org_123" to "acme-insurance"
    When another org tries to take the handle "acme"
    Then the change is rejected with "handle unavailable"

  Scenario: Admin calls through an alias pass the org-match check
    Given the org admin changed the handle of "org_123" to "acme-insurance"
    And I am "alice" in org "org_123" with role "org:member"
    When I call admin "GET" "https://shadowmock.test/acme/__admin/sessions"
    Then the response status is 200
