@saas @P5 @FR-9.6b @FR-9.6 @NFR-5.2 @TA-15
Feature: What is public on a shared host, and the real client address
  The admin API is reachable from the internet, so a credential is its only gate. Health and readiness are public;
  metrics can reveal org handles and per-org volumes, so they are not. The allowlist and the rate limit use the
  address of the real client, never one the client supplied.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"

  Scenario Outline: Public and protected routes
    When I request "<path>" without a credential
    Then the response status is <status>

    Examples:
      | path                    | status |
      | /__health               | 200    |
      | /__ready                | 200    |
      | /__metrics              | 401    |
      | /acme/__admin/sessions  | 401    |
      | /acme/hello             | 404    |

  Scenario: Metrics need a credential
    When I request "/__metrics" with a valid token
    Then the response status is 200
    And no metric carries an org handle as a label value

  Scenario: Metrics can be limited to the private network instead
    Given metrics are served on a private listener
    When I request "/__metrics" on the public listener
    Then the response status is 404

  Scenario: The admin API is not trusted because of where the call came from
    When I call admin "GET" "/sessions" from "10.0.0.5" without a credential
    Then the response status is 401
    When I call admin "GET" "/sessions" from "127.0.0.1" without a credential
    Then the response status is 401

  Scenario Outline: The client address is the right-most entry a trusted proxy added
    Given the number of trusted hops is <hops>
    When a request arrives with "X-Forwarded-For: <header>" and the peer "<peer>"
    Then the client address the engine uses is "<client>"

    Examples:
      | hops | header                        | peer     | client       |
      | 1    | 203.0.113.4                   | 10.1.1.1 | 203.0.113.4  |
      | 1    | 6.6.6.6, 203.0.113.4          | 10.1.1.1 | 203.0.113.4  |
      | 2    | 6.6.6.6, 203.0.113.4, 10.2.2.2 | 10.1.1.1 | 203.0.113.4 |
      | 1    |                               | 10.1.1.1 | 10.1.1.1     |

  Scenario: A client-supplied header is never trusted on its own
    Given the number of trusted hops is 1
    When a request arrives straight from "198.51.100.9" with "X-Forwarded-For: 203.0.113.4"
    Then the client address the engine uses is "198.51.100.9"

  Scenario: The allowlist and the rate limit use that address
    Given the org's IP allowlist is "203.0.113.0/24"
    When a request arrives with "X-Forwarded-For: 203.0.113.4" through one trusted hop
    Then it is allowed
    When a request arrives with "X-Forwarded-For: 203.0.113.4, 198.51.100.9" through one trusted hop
    Then it is rejected with "ip_not_allowed"

  Scenario: The number of trusted hops is set per environment
    Given the environment "staging" has 1 trusted hop and "production" has 2
    Then each uses its own count
