@saas @P5 @NFR-4.3
Feature: Usage metering for billing
  The engine records per-org daily meters. They are reported to the platform
  billing service once a day, idempotently.

  Background:
    Given Shadow Mock is running in SaaS mode with 2 instances
    And the org "acme" with handle "acme" on plan "Team"
    And org "acme" has the global mapping:
      """json
      { "request": { "urlPath": "/m" }, "response": { "status": 200 } }
      """

  Scenario: mock_requests counts every served request across instances
    When I send 600 requests to instance 1 and 400 requests to instance 2
    And the meters are flushed
    Then the org "acme" meter "mock_requests" for today is 1000

  Scenario Outline: What counts as a billable mock request
    When <action>
    And the meters are flushed
    Then the org "acme" meter "mock_requests" increases by <delta>

    Examples:
      | action                                                        | delta |
      | I send a "GET" request to "/m"                                 | 1     |
      | I send a "GET" request to "/unmatched"                         | 1     |
      | I call admin "GET" "/sessions"                                 | 0     |
      | I send a request rejected by the IP allowlist                  | 0     |
      | I send a request rejected by the rate limit                    | 0     |

  Scenario: Concurrent sessions are billed at the monthly 95th percentile
    Given the test clock is at the start of the billing month
    When org "acme" has 10 active sessions for 95% of the month and 100 for 5% of the month
    Then the org "acme" billable "concurrent_sessions_p95" for the month is 10

  Scenario: URL-based and key-based sessions both count
    Given org "acme" has 3 active key-based and 2 active URL-based shadow mocks for the whole day
    Then every 5-minute concurrent-session sample for org "acme" today is 5

  Scenario: Visibility meters are recorded
    Given org "acme" creates 4 sessions and keeps each for 30 minutes
    Then the org "acme" meter "sessions_created" for today is 4
    And the org "acme" meter "session_minutes" for today is 120

  Scenario: Daily reporting is idempotent
    Given the daily usage report for org "acme" for yesterday has been sent
    When the report job runs again for yesterday
    Then the billing provider holds exactly one usage record per meter for org "acme" for yesterday

  Scenario: Usage is visible through the admin API
    Given I am "alice" in org "acme" with role "org:member"
    When I call admin "GET" "/usage"
    Then the response JSON at "$.period.mockRequests.used" is a number
    And the response JSON at "$.period.mockRequests.included" is 15000000
    And the response JSON at "$.period.concurrentSessionsP95.included" is 50
