@saas @P5 @NFR-5.3 @security
Feature: Egress policy for proxying and webhooks
  Proxy and webhook targets pass an allow/deny policy that blocks private,
  link-local and metadata addresses, including after DNS resolution.

  Background:
    Given Shadow Mock is running in SaaS mode
    And the org "acme" with handle "acme" on plan "Team"
    And a key-based shadow mock "S1"

  Scenario Outline: Blocked destinations
    Given the mapping in "S1":
      """json
      { "request": { "urlPath": "/p" }, "response": { "proxyBaseUrl": "<target>" } }
      """
    When I send a "GET" request to "/p" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 502
    And the response JSON at "$.error" is "egress_blocked"

    Examples:
      | target                         |
      | http://169.254.169.254         |
      | http://10.1.2.3                |
      | http://172.16.0.1              |
      | http://192.168.0.1             |
      | http://127.0.0.1:6379          |
      | http://[::1]:5432              |
      | http://internal.rebind.test    |

  Scenario: A public destination is allowed
    Given the mapping in "S1":
      """json
      { "request": { "urlPath": "/p" }, "response": { "proxyBaseUrl": "https://api.public-upstream.test" } }
      """
    When I send a "GET" request to "/p" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the request was forwarded to "https://api.public-upstream.test/p"

  Scenario: Redirects to private addresses are not followed
    Given a public upstream that redirects to "http://169.254.169.254/latest"
    And the mapping in "S1" proxies "/r" to that upstream
    When I send a "GET" request to "/r" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the engine made no request to "169.254.169.254"
