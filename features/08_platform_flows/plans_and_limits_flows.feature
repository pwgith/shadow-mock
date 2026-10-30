@saas @P5 @flow-F9 @flow-F15 @flow-F16 @flow-F18 @flow-F22 @flow-F24 @FR-11.5 @FR-11.6 @FR-11.12 @TA-14 @TA-16
Feature: The engine's part of the plan, payment and limit flows
  The platform decides plans, trials, payment and holds. What reaches the engine is configuration: a plan, limits and
  a status in the org's rows. These are the effects those rows must have.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"
    And the global layer has "GET /hello" returning "hi"

  # F9: an upgrade reaches the engine.
  Scenario: F9 - A plan change is applied within a second
    Given the plan allows 5 concurrent shadow mocks and 10 requests per second
    When the management app writes a plan allowing 50 shadow mocks and 100 requests per second
    Then within 1 second the 6th shadow mock can be created
    And 50 requests in one second are served

  # F15: reaching a limit, and moving to a smaller plan.
  Scenario: F15 - A limit refuses the action and names the limit
    Given the plan allows 2 concurrent shadow mocks and 2 are active
    When I create another shadow mock
    Then the response status is 429
    And the response JSON at "$.error" is "session_limit_reached"
    And the existing shadow mocks are unaffected

  Scenario: F15 - A smaller plan deletes nothing
    Given org "acme" has 5 shadow mocks, 40 mappings and 3 files
    When the management app writes a plan allowing 2 shadow mocks and 10 mappings per shadow mock
    Then the 5 shadow mocks, 40 mappings and 3 files are all still there and still served
    But creating another shadow mock is refused

  Scenario: F15 - A feature the smaller plan lacks is switched off and its settings kept
    Given the org's IP allowlist is "198.51.100.0/24"
    When the management app writes a plan without the IP allowlist
    Then the allowlist is not applied
    And the entries are still stored
    When the management app writes a plan with the allowlist again
    Then the allowlist applies again without being entered again

  # F16: payment fails, then recovers or the subscription is cancelled.
  Scenario: F16 - Past due still serves
    When the management app sets the status of org "acme" to "past_due"
    Then "GET /acme/hello" is served

  Scenario: F16 - Suspended refuses, then serves again when the status is lifted
    When the management app sets the status of org "acme" to "suspended"
    Then "GET /acme/hello" is answered 403 with "org_suspended"
    When the management app sets the status of org "acme" to "active"
    Then within 1 second "GET /acme/hello" is served
    And no mapping, shadow mock or setting was lost

  Scenario: F16 - A cancelled org stays suspended with its data until deletion
    When the management app sets the status of org "acme" to "cancelled"
    Then "GET /acme/hello" is answered 403 with "org_suspended"
    And the org's mappings are still stored
    When the management app subscribes the org again within 30 days
    Then "GET /acme/hello" is served

  # F18: staff hold.
  Scenario: F18 - A hold is a suspension the engine cannot tell from non-payment
    When the management app marks org "acme" suspended for an administrative hold
    Then "GET /acme/hello" is answered 403 with "org_suspended"
    And its Tool app keys and tokens are refused
    # That a payment cannot clear it is decided by the platform before the row is written.

  # F22: a runaway test.
  Scenario: F22 - A runaway test is limited, counted apart, and reported
    Given the org is limited to 10 requests per second
    When a test sends 5000 requests in one second
    Then most are answered 429 with "rate_limited", "Retry-After" and "X-Shadow-Mock-Limit"
    And "GET /globex/hello" for another org is unaffected
    And the usage endpoint's "rateLimited" for the hour is the number of 429 answers
    And its "requests" counts only the requests that were served

  Scenario: F22 - The management app can show how many were limited
    Given 120 requests were rate limited in the last hour
    When I call admin "GET" "/usage" with a token for "alice" in org "acme"
    Then the current hour's "rateLimited" is 120

  # F24: a trial ends without a card.
  Scenario: F24 - A trial that ends suspends the org and keeps its data
    When the management app sets the status of org "acme" to "suspended" at the end of a trial
    Then "GET /acme/hello" is answered 403
    And the org's configuration is kept
