@saas @P5 @FR-9.6b @TA-15
Feature: Per-org IP allowlist for mock traffic
  Org admins can restrict calls to their mocks to a set of source IP ranges.
  The check runs before the body is read, detection or matching.

  Background:
    Given Shadow Mock is running in SaaS mode
    And the org "acme" with handle "acme" on plan "Team"
    And org "acme" has the global mapping:
      """json
      { "request": { "urlPathPattern": "/.*" }, "response": { "status": 200 } }
      """

  Scenario: An empty allowlist allows every source IP
    When I send a "GET" request to "/x" from IP "198.51.100.77"
    Then the response status is 200

  Scenario Outline: Only listed ranges are allowed
    Given the org "acme" IP allowlist is:
      | cidr             | label             |
      | 203.0.113.0/24   | Test env NAT      |
      | 2001:db8:1::/48  | IPv6 test network |
      | 192.0.2.10/32    | Jenkins agent     |
    When I send a "GET" request to "/x" from IP "<ip>"
    Then the response status is <status>

    Examples:
      | ip                | status |
      | 203.0.113.45      | 200    |
      | 203.0.114.1       | 403    |
      | 2001:db8:1::abcd  | 200    |
      | 2001:db8:2::1     | 403    |
      | 192.0.2.10        | 200    |
      | 192.0.2.11        | 403    |
      | ::ffff:203.0.113.9| 200    |

  Scenario: A rejection explains itself and is counted but not journaled or billed
    Given the org "acme" IP allowlist is:
      | cidr           |
      | 203.0.113.0/24 |
    When I send a "GET" request to "/x" from IP "198.51.100.77"
    Then the response status is 403
    And the response JSON at "$.error" is "ip_not_allowed"
    And the response JSON at "$.ip" is "198.51.100.77"
    And the org "acme" stat "requests_rejected_ip" increases by 1
    And the org "acme" meter "mock_requests" does not increase
    And the journal has no entry for that request

  Scenario: The allowlist applies to URL-based and key-based traffic too
    Given the org "acme" IP allowlist is:
      | cidr           |
      | 203.0.113.0/24 |
    And a URL-based shadow mock "U1"
    When I send a "GET" request to "/x" via "U1" from IP "198.51.100.77"
    Then the response status is 403

  Scenario Outline: applyToAdmin also guards the admin API
    Given the org "acme" IP allowlist is:
      | cidr           |
      | 203.0.113.0/24 |
    And the org "acme" has "applyToAdmin" set to <apply>
    And I am "alice" in org "acme" with app role "member"
    When I call admin "GET" "/sessions" from IP "198.51.100.77"
    Then the response status is <status>

    Examples:
      | apply | status |
      | false | 200    |
      | true  | 403    |

  Scenario: The rejected request's body is never read
    Given the org "acme" IP allowlist is:
      | cidr           |
      | 203.0.113.0/24 |
    When I send a "POST" request with a 50 MB body to "/x" from IP "198.51.100.77"
    Then the response status is 403
    And the engine read 0 bytes of the request body

  Scenario: Allowlist changes take effect without a restart
    Given the org "acme" IP allowlist is:
      | cidr           |
      | 203.0.113.0/24 |
    When the org admin adds "198.51.100.0/24" in the management app
    Then within 1 s a "GET" request to "/x" from IP "198.51.100.77" returns status 200

  Scenario: The allowlist is a plan entitlement
    Given the org "acme" is on Shadow Mock plan "Starter"
    When the org admin tries to add an IP allowlist entry
    Then the change is rejected with "upgrade to Team"

  Scenario: Tokens from the management app are exempt from the admin allowlist, keys are not
    Given the org's IP allowlist is "198.51.100.0/24" and applies to the admin API
    When the management app calls admin "GET" "/sessions" with a token it signed, from the host's servers at "203.0.113.4"
    Then the response status is 200
    When a pipeline calls admin "GET" "/sessions" with a Tool app key from "203.0.113.4"
    Then the response status is 403
    And the response JSON at "$.error" is "ip_not_allowed"

  Scenario: Mock traffic is never exempt
    Given the org's IP allowlist is "198.51.100.0/24"
    When the SUT sends "GET /acme/hello" from "203.0.113.4"
    Then the response status is 403
