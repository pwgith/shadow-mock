@wiremock @P3 @FR-3.5
Feature: Proxying to a real upstream

  Background:
    Given Shadow Mock is running
    And the upstream stub server "payments" returns 200 with body "real" for "GET /payments/1"

  Scenario: proxyBaseUrl forwards the request and returns the upstream response
    Given the global mapping:
      """json
      { "request": { "urlPathPattern": "/payments/.*" }, "response": { "proxyBaseUrl": "${upstream.payments}" } }
      """
    When I send a "GET" request to "/payments/1"
    Then the response body is "real"
    And the upstream "payments" received "GET /payments/1"

  Scenario: Adding and removing request headers on the way through
    Given the global mapping:
      """json
      { "request": { "urlPathPattern": "/payments/.*" },
        "response": { "proxyBaseUrl": "${upstream.payments}",
                      "additionalProxyRequestHeaders": { "X-Proxied-By": "shadow-mock" },
                      "removeProxyRequestHeaders": ["X-Secret"] } }
      """
    When I send a request:
      | method | path        | header:X-Secret |
      | GET    | /payments/1 | hide-me         |
    Then the upstream "payments" received header "X-Proxied-By" with value "shadow-mock"
    And the upstream "payments" did not receive header "X-Secret"

  Scenario: proxyUrlPrefixToRemove strips a prefix before forwarding
    Given the global mapping:
      """json
      { "request": { "urlPathPattern": "/api/payments/.*" },
        "response": { "proxyBaseUrl": "${upstream.payments}", "proxyUrlPrefixToRemove": "/api" } }
      """
    When I send a "GET" request to "/api/payments/1"
    Then the upstream "payments" received "GET /payments/1"

  Scenario: Partial mocking: specific stubs win, everything else is proxied
    Given the global mapping:
      """json
      { "priority": 10, "request": { "urlPathPattern": "/payments/.*" }, "response": { "proxyBaseUrl": "${upstream.payments}" } }
      """
    And the global mapping:
      """json
      { "priority": 1, "request": { "urlPath": "/payments/declined" }, "response": { "status": 402 } }
      """
    When I send a "GET" request to "/payments/declined"
    Then the response status is 402
    When I send a "GET" request to "/payments/1"
    Then the response body is "real"

  @saas @security @NFR-5.3
  Scenario Outline: Proxy targets in private or metadata ranges are blocked in SaaS
    Given the session mapping in a key-based shadow mock "S1":
      """json
      { "request": { "urlPath": "/p" }, "response": { "proxyBaseUrl": "<target>" } }
      """
    When I send a "GET" request to "/p" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 502
    And the response JSON at "$.error" is "egress_blocked"

    Examples:
      | target                    |
      | http://169.254.169.254    |
      | http://10.0.0.5           |
      | http://localhost:5432     |

  Scenario: A proxy target can be templated
    Given the global mapping:
      """json
      { "request": { "urlPathPattern": "/payments/.*" },
        "response": { "proxyBaseUrl": "{{request.headers.X-Target}}", "transformers": ["response-template"] } }
      """
    When I send a request:
      | method | path        | header:X-Target       |
      | GET    | /payments/1 | ${upstream.payments}  |
    Then the response body is "real"

  @FR-10.11
  Scenario: An unreachable upstream is a 500 that says so, as in WireMock
    Given the upstream stub server "payments" is stopped
    And the global mapping:
      """json
      { "request": { "urlPathPattern": "/payments/.*" }, "response": { "proxyBaseUrl": "${upstream.payments}" } }
      """
    When I send a "GET" request to "/payments/1"
    Then the response status is 500
    And the response body contains "Network failure trying to make a proxied request"
