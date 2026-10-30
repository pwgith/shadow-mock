@wiremock @P3 @standalone @FR-10.15 @FR-10.15a @FR-10.15b @FR-10.15c @FR-10.15d @FR-10.15e
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

  Scenario: An HTTPS request is answered inside a tunnel the proxy opens up
    Given Shadow Mock is running as a browser proxy
    And the global mapping:
      """json
      { "request": { "urlPath": "/secure" }, "response": { "status": 200, "body": "inside the tunnel" } }
      """
    When I send a "GET" request to "https://anywhere.example/secure" through the proxy
    Then the response body equals 'inside the tunnel'
    When I call admin "GET" "/requests"
    Then the response JSON at "$.requests[0].request.scheme" is "https"
    And the response JSON at "$.requests[0].request.host" is "anywhere.example"
    And the response JSON at "$.requests[0].request.browserProxyRequest" is true

  Scenario: The authority that signs the certificates is made once and stored for the client to trust
    Given Shadow Mock is running as a browser proxy
    Then the certificate authority is stored as "ca.pem"
    And the certificate authority is stored as "ca-keystore.p12"

  Scenario: A tunnel is refused when browser proxying is off
    Given Shadow Mock is running
    When I send a "GET" request to "https://anywhere.example/secure" through the proxy
    Then the request fails with an error mentioning "Method Not Allowed"

  Scenario: An origin whose certificate nobody trusts is an SSL failure
    Given Shadow Mock is running as a browser proxy
    And the upstream stub server "secure" uses HTTPS with a certificate nobody trusts
    And the upstream stub server "secure" returns 200 with body "from the secure origin" for "GET /real"
    When I send a "GET" request to "${upstream.secure}/real" through the proxy
    Then the response status is 500
    And the response body contains "SSL failure trying to make a proxied request"

  Scenario: Every origin can be trusted
    Given Shadow Mock is running as a browser proxy that trusts every origin
    And the upstream stub server "secure" uses HTTPS with a certificate nobody trusts
    And the upstream stub server "secure" returns 200 with body "from the secure origin" for "GET /real"
    When I send a "GET" request to "${upstream.secure}/real" through the proxy
    Then the response body equals 'from the secure origin'

  Scenario: One origin can be trusted by name, and another is still checked
    Given Shadow Mock is running as a browser proxy that trusts the origin "127.0.0.1"
    And the upstream stub server "secure" uses HTTPS with a certificate nobody trusts
    And the upstream stub server "secure" returns 200 with body "from the secure origin" for "GET /real"
    When I send a "GET" request to "${upstream.secure}/real" through the proxy
    Then the response body equals 'from the secure origin'

  Scenario: Only the named origin is trusted
    Given Shadow Mock is running as a browser proxy that trusts the origin "other.example"
    And the upstream stub server "secure" uses HTTPS with a certificate nobody trusts
    And the upstream stub server "secure" returns 200 with body "from the secure origin" for "GET /real"
    When I send a "GET" request to "${upstream.secure}/real" through the proxy
    Then the response status is 500

  Scenario: The proxy sends on what it is told to send on, and only to targets the rules allow
    Given Shadow Mock is running with configuration:
      """yaml
      proxy:
        denyTargets: [ "127.0.0.1" ]
        browser: { enabled: true }
      """
    And the upstream stub server "origin" returns 200 with body "from the origin" for "GET /real"
    When I send a "GET" request to "${upstream.origin}/real" through the proxy
    Then the response status is 500
    And the response body contains "denied"

  Scenario Outline: Settings that cannot work are refused at start-up
    Given the configuration file contains:
      """yaml
      <settings>
      """
    When Shadow Mock starts
    Then start-up fails with an error mentioning "<mentions>"

    Examples:
      | settings                                                                          | mentions                          |
      | proxy: { browser: { caKeystore: ca.p12 } }                                        | proxy.browser.enabled             |
      | proxy: { browser: { caDir: /tmp/ca } }                                            | proxy.browser.enabled             |
      | proxy: { browser: { trustAll: true } }                                            | proxy.browser.enabled             |
      | proxy: { browser: { enabled: true, caKeystoreType: BKS } }                        | JKS or PKCS12                     |
      | proxy: { browser: { enabled: true, trustTargets: ['http://x/'] } }                | not a host name                   |
      | proxy: { via: 'p:3128', browser: { enabled: true, trustAll: true } }              | cannot be combined with proxy.via |

  Scenario: A certificate authority keystore that is not there stops start-up
    Then starting Shadow Mock with configuration fails with an error mentioning "caKeystore":
      """yaml
      proxy:
        browser: { enabled: true, caKeystore: /nonexistent/ca.p12 }
      """

  Scenario: A keystore whose certificate is not a certificate authority stops start-up
    Given a PKCS12 keystore with password "password" is available as "notca"
    Then starting Shadow Mock with configuration fails with an error mentioning "not a certificate authority":
      """yaml
      proxy:
        browser: { enabled: true, caKeystore: '${notca}', caKeystoreType: PKCS12 }
      """
