@saas @P5 @FR-9.6b @security
Feature: Determining the true client IP behind the platform proxy
  The client IP comes only from the X-Forwarded-For hop added by the trusted
  proxy, so a client cannot spoof it.

  Background:
    Given Shadow Mock is running in SaaS mode with "server.clientIp" set to header "X-Forwarded-For" and trustedHops 1
    And the org "acme" IP allowlist is:
      | cidr           |
      | 203.0.113.0/24 |
    And org "acme" has the global mapping:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """

  Scenario Outline: Client-supplied X-Forwarded-For entries are ignored
    When the proxy simulator forwards a request from real IP "<real>" carrying client header "X-Forwarded-For: <spoofed>"
    Then the engine sees client IP "<real>"
    And the response status is <status>

    Examples:
      | real          | spoofed        | status |
      | 198.51.100.7  | 203.0.113.5    | 403    |
      | 203.0.113.5   | 198.51.100.7   | 200    |

  Scenario: A request reaching the engine without the proxy uses the TCP peer
    Given a request reaches the engine directly from "10.0.0.9" with no X-Forwarded-For header
    Then the engine sees client IP "10.0.0.9"

  Scenario: The client IP source is configurable for other hosts
    Given Shadow Mock is running in SaaS mode with "server.clientIp" set to header "Fly-Client-IP"
    When a request arrives with header "Fly-Client-IP: 203.0.113.8"
    Then the engine sees client IP "203.0.113.8"
