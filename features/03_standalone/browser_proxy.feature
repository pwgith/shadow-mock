@wiremock @P3 @standalone @FR-10.15
Feature: Using Shadow Mock as a browser proxy
  With proxy.browser.enabled, or WireMock's --enable-browser-proxying, a browser or HTTP client can be
  pointed at Shadow Mock as its proxy server. A request sent through it is matched against the mappings
  as it would be if it were sent to Shadow Mock, whatever host it names, and one no mapping matches goes
  on to the host it named. For HTTPS the client sends CONNECT and Shadow Mock ends the TLS itself, with a
  certificate it makes for the host and signs with a certificate authority the client has been told to
  trust. The tunnel itself, the certificate checks and the authority are covered by
  cmd/shadowmock/browserproxy_test.go and internal/browserproxy; every expectation here was checked
  against a real WireMock 3.13.2 (test/conformance/browserproxy_differential_test.go).

  Scenario: A mapping answers a request for any host
    Given Shadow Mock is running as a browser proxy
    And the global mapping:
      """json
      { "request": { "urlPath": "/hello" }, "response": { "status": 200, "body": "hi" } }
      """
    When I send a "GET" request to "http://anywhere.example/hello" through the proxy
    Then the response status is 200
    And the response body equals 'hi'

  Scenario: A request no mapping matches goes on to its origin
    Given Shadow Mock is running as a browser proxy
    And the upstream stub server "origin" returns 200 with body "from the origin" for "GET /real"
    When I send a "GET" request to "${upstream.origin}/real" through the proxy
    Then the response body equals 'from the origin'
    And the upstream "origin" received "GET /real"

  Scenario: The journal says the request came through the proxy, and counts the pass-through as matched
    Given Shadow Mock is running as a browser proxy
    And the upstream stub server "origin" returns 200 with body "from the origin" for "GET /real"
    When I send a "GET" request to "${upstream.origin}/real" through the proxy
    And I call admin "GET" "/requests"
    Then the response JSON at "$.requests[0].request.browserProxyRequest" is true
    And the response JSON at "$.requests[0].wasMatched" is true

  Scenario: A request sent straight to Shadow Mock is not a proxy request
    Given Shadow Mock is running as a browser proxy
    And the global mapping:
      """json
      { "request": { "urlPath": "/hello" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/hello"
    And I call admin "GET" "/requests"
    Then the response JSON at "$.requests[0].request.browserProxyRequest" is false

  Scenario: An origin that cannot be reached is a 500 that says so
    Given Shadow Mock is running as a browser proxy
    And the upstream stub server "origin" returns 200 with body "unused" for "GET /real"
    And the upstream stub server "origin" is stopped
    When I send a "GET" request to "${upstream.origin}/real" through the proxy
    Then the response status is 500
    And the response body contains "Network failure trying to make a proxied request"

  Scenario: Without browser proxying a request for another host is a plain request
    Given Shadow Mock is running
    When I send a "GET" request to "http://anywhere.example/hello" through the proxy
    And I call admin "GET" "/requests"
    Then the response JSON at "$.requests[0].request.browserProxyRequest" is false
