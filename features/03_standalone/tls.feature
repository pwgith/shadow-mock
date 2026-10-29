@wiremock @P3 @standalone @FR-10.14
Feature: HTTPS and HTTP/2
  With tls.port, or WireMock's --https-port, the server also listens for HTTPS, with a certificate it makes
  for itself when none is configured. Over TLS it speaks HTTP/2 as well as HTTP/1.1, and without TLS it
  accepts HTTP/2 on prior knowledge (h2c); tls.disableHttp2Tls and tls.disableHttp2Plain switch each off.
  Starting the real listeners, the keystore formats, the truststore and the client certificates are covered
  by cmd/shadowmock/tls_test.go and internal/tlsconf; every expectation here was checked against a real
  WireMock 3.13.2 (test/conformance/tls_differential_test.go).

  Scenario: The HTTPS listener serves the same mappings
    Given Shadow Mock is running with configuration:
      """yaml
      tls:
        port: 8443
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/hello" }, "response": { "status": 200, "body": "hi" } }
      """
    When I send a "GET" request to "https://localhost:8443/hello"
    Then the response status is 200
    And the response body equals 'hi'

  Scenario: A request over TLS is served with HTTP/2 and says so in the journal
    Given Shadow Mock is running with configuration:
      """yaml
      tls:
        port: 8443
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/hello" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "https://localhost:8443/hello"
    And I call admin "POST" "/requests/find" with:
      """json
      { "urlPath": "/hello" }
      """
    Then the response JSON at "$.requests[0].protocol" is "HTTP/2.0"
    And the response JSON at "$.requests[0].scheme" is "https"

  Scenario: HTTP/2 over TLS can be switched off
    Given Shadow Mock is running with configuration:
      """yaml
      tls:
        port: 8443
        disableHttp2Tls: true
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/hello" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "https://localhost:8443/hello"
    And I call admin "POST" "/requests/find" with:
      """json
      { "urlPath": "/hello" }
      """
    Then the response JSON at "$.requests[0].protocol" is "HTTP/1.1"

  Scenario: The admin API can be limited to HTTPS
    Given Shadow Mock is running with configuration:
      """yaml
      tls:
        port: 8443
        adminRequireHttps: true
      """
    When I send a "GET" request to "/__admin/mappings"
    Then the response status is 403
    And the response JSON at "$.errors[0].code" is 40
    And the response JSON at "$.errors[0].title" is "HTTPS is required for accessing the admin API"
    When I send a "GET" request to "https://localhost:8443/__admin/mappings"
    Then the response status is 200

  Scenario: Mock traffic is not limited to HTTPS
    Given Shadow Mock is running with configuration:
      """yaml
      tls:
        port: 8443
        adminRequireHttps: true
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/open" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/open"
    Then the response status is 200

  Scenario: The HTTPS port must differ from the plain one
    Given the configuration file contains:
      """yaml
      tls: { port: 8080 }
      """
    When Shadow Mock starts
    Then start-up fails with an error mentioning "is also the port of listen.mock"

  Scenario: Requiring client certificates needs a truststore
    Given the configuration file contains:
      """yaml
      tls: { port: 8443, requireClientCert: true }
      """
    When Shadow Mock starts
    Then start-up fails with an error mentioning "tls.requireClientCert needs tls.truststore"
