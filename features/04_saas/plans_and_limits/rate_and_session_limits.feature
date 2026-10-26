@saas @P5 @NFR-4.2
Feature: Plan limits on request rate and sessions
  Limits come from the org's plan entitlements. Soft caps bill overage and
  never break a running test. Hard caps return 429 with a reason.

  Background:
    Given Shadow Mock is running in SaaS mode
    And the org "acme" with handle "acme"
    And org "acme" has the global mapping:
      """json
      { "request": { "urlPathPattern": "/.*" }, "response": { "status": 200 } }
      """

  Scenario Outline: Request rate limits per plan
    Given the org "acme" is on Shadow Mock plan "<plan>"
    When I send <rps> requests per second for 5 seconds
    Then about <limited> requests per second receive 429 with error "rate_limited"
    And rate-limited requests are not counted in "mock_requests"

    Examples:
      | plan     | rps  | limited |
      | Free     | 15   | 5       |
      | Starter  | 50   | 0       |
      | Team     | 250  | 50      |
      | Business | 1000 | 0       |

  Scenario: The Free tier hard-caps monthly requests
    Given the org "acme" is on Shadow Mock plan "Free"
    And org "acme" has served 100000 mock requests this month
    When I send a "GET" request to "/x"
    Then the response status is 429
    And the response JSON at "$.error" is "monthly_request_cap"

  Scenario: Paid tiers keep serving past the included requests and bill overage
    Given the org "acme" is on Shadow Mock plan "Starter"
    And org "acme" has served 2000000 mock requests this month
    When I send a "GET" request to "/x"
    Then the response status is 200
    And the org "acme" usage shows overage requests

  Scenario Outline: Concurrent session limits
    Given the org "acme" is on Shadow Mock plan "<plan>"
    And org "acme" has <active> active sessions
    When I call admin "POST" "/sessions/key-based" with:
      """json
      {}
      """
    Then the response status is <status>

    Examples:
      | plan    | active | status |
      | Free    | 1      | 201    |
      | Free    | 2      | 429    |
      | Starter | 10     | 201    |
      | Starter | 29     | 201    |
      | Starter | 30     | 429    |

  Scenario: Going over the included sessions is billed but allowed up to the ceiling
    Given the org "acme" is on Shadow Mock plan "Team"
    And org "acme" has 50 active sessions
    When I create a key-based shadow mock
    Then the response status is 201
    And the response header "X-Shadow-Mock-Usage-Warning" is "sessions_over_included"

  Scenario Outline: CI key limits
    Given the org "acme" is on Shadow Mock plan "<plan>"
    And org "acme" has <keys> active CI keys
    When an org admin creates another CI key
    Then the result is "<result>"

    Examples:
      | plan    | keys | result                 |
      | Free    | 1    | rejected: limit reached |
      | Starter | 4    | created                |
      | Team    | 25   | rejected: limit reached |

  Scenario: Limits change immediately when the plan changes
    Given the org "acme" is on Shadow Mock plan "Free"
    When the platform upgrades org "acme" to Shadow Mock plan "Team"
    Then within 1 s the engine enforces the Team limits for org "acme"
