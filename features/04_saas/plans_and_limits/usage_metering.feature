@saas @P5 @FR-11.12 @NFR-4.3
Feature: Usage the management app can read
  The engine counts what happened and answers `GET /__admin/usage` with hourly buckets. The management app reads
  them and reports to the platform; the engine does not know the platform exists.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"
    And a mapping "hello" in the global layer for "GET /hello"

  Scenario: Requests are counted in hourly buckets across instances
    Given 2 instances are running
    When the SUT sends 30 requests to instance 1 and 20 to instance 2 in the same hour
    And I call admin "GET" "/usage" with a token for "alice" in org "acme"
    Then the bucket for that hour has "requests" 50
    And the bucket has a "periodStart", a "periodEnd" and a "revision"

  Scenario Outline: What counts as a billable mock request
    When <event>
    And I call admin "GET" "/usage" with a token for "alice" in org "acme"
    Then the "requests" of the hour <counts>

    Examples:
      | event                                          | counts                |
      | the SUT sends "GET /acme/hello" and it is served | goes up by 1          |
      | the SUT sends "GET /acme/unknown" and nothing matches | goes up by 1     |
      | I call admin "GET" "/sessions" with a token    | does not change       |
      | the SUT is rejected by the IP allowlist        | does not change       |
      | the SUT is rate limited                        | does not change       |
      | the SUT is rejected as suspended               | does not change       |

  Scenario: The busiest moment of concurrent sessions is reported for each hour
    Given 3 sessions are active at 10:05 and 7 at 10:40 and 2 at 10:55
    When I call admin "GET" "/usage" with a token for "alice" in org "acme"
    Then the bucket for 10:00 has "concurrentSessionsMax" 7

  Scenario: URL-based and key-based sessions both count
    Given 2 key-based and 3 URL-based shadow mocks are active
    When I call admin "GET" "/usage" with a token for "alice" in org "acme"
    Then the current bucket has "concurrentSessionsMax" 5

  Scenario: Stored files are reported as their peak
    Given the org stores 3 files totalling 1500000 bytes
    When I call admin "GET" "/usage" with a token for "alice" in org "acme"
    Then the current bucket has "storedFiles" with count 3 and bytes 1500000

  Scenario: Rate limited and rejected requests are visible, not billed
    When the SUT is rate limited 7 times and rejected by the allowlist 4 times
    And I call admin "GET" "/usage" with a token for "alice" in org "acme"
    Then the current bucket has "rateLimited" 7 and "ipRejected" 4

  Scenario: Late data is a higher revision of the same bucket
    Given a bucket was read at revision 1
    When late requests for that hour are counted
    And I call admin "GET" "/usage" with a token for "alice" in org "acme"
    Then the bucket has the same "periodStart" and a higher "revision"

  Scenario: A range can be asked for
    When I call admin "GET" "/usage?from=2026-10-30T09:00:00Z&to=2026-10-30T12:00:00Z" with a token for "alice" in org "acme"
    Then the response has one bucket for each hour in the range

  Scenario: A key with the read scope can read usage, and the metadata scope can too
    When I call admin "GET" "/usage" with a key that has the scope "read"
    Then the response status is 200
    When I call admin "GET" "/usage" with a token that has the scope "metadata"
    Then the response status is 200

  Scenario: One org cannot read another's usage
    Given the org "globex" with handle "globex" on plan "Team"
    When I call admin "GET" "/usage" on org "globex" with a token for "alice" in org "acme"
    Then the response status is 403

  Scenario: Standalone has no usage
    Given Shadow Mock is running in standalone mode
    When I call admin "GET" "/usage"
    Then the response status is 404
