@wiremock @P3 @standalone @FR-10.14 @FR-10.14a @FR-10.14b @FR-10.14c @FR-10.14d @FR-10.14e
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

  Scenario Outline: The certificate comes from a keystore or from PEM files
    Given <material>
    And Shadow Mock is running with configuration:
      """yaml
      <settings>
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/hello" }, "response": { "status": 200, "body": "hi" } }
      """
    When I send a "GET" request to "https://localhost:8443/hello"
    Then the response status is 200
    And the response body equals 'hi'

    Examples:
      | material                                                           | settings                                                                                  |
      | a PKCS12 keystore with password "s3cret" is available as "store"   | tls: { port: 8443, keystore: '${store}', keystoreType: PKCS12, keystorePassword: s3cret } |
      | a PKCS12 keystore with password "password" is available as "store" | tls: { port: 8443, keystore: '${store}' }                                                 |
      | a JKS keystore with password "s3cret" is available as "store"      | tls: { port: 8443, keystore: '${store}', keystoreType: JKS, keystorePassword: s3cret }    |
      | a JKS keystore with password "password" is available as "store"    | tls: { port: 8443, keystore: '${store}' }                                                 |
      | a PEM certificate and key are available as "crt" and "key"         | tls: { port: 8443, certFile: '${crt}', keyFile: '${key}' }                                |

  Scenario Outline: A keystore that cannot be opened stops start-up and says which setting
    Given a <kind> keystore with password "right" is available as "store"
    Then starting Shadow Mock with configuration fails with an error mentioning "tls.keystore":
      """yaml
      tls:
        port: 8443
        keystore: '${store}'
        keystoreType: <kind>
        keystorePassword: wrong
      """

    Examples:
      | kind   |
      | PKCS12 |
      | JKS    |

  Scenario: A missing certificate file stops start-up
    Then starting Shadow Mock with configuration fails with an error mentioning "tls.certFile":
      """yaml
      tls:
        port: 8443
        certFile: /nonexistent/cert.pem
        keyFile: /nonexistent/key.pem
      """

  Scenario Outline: Client certificates are checked against a truststore
    Given a client certificate is trusted through the <kind> truststore "trust" with password "tp"
    And Shadow Mock is running with configuration:
      """yaml
      tls:
        port: 8443
        truststore: '${trust}'
        truststoreType: <kind>
        truststorePassword: tp
        requireClientCert: true
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/hello" }, "response": { "status": 200, "body": "hi" } }
      """
    When requests are sent with the client certificate
    And I send a "GET" request to "https://localhost:8443/hello"
    Then the response status is 200
    When requests are sent with no client certificate
    And I send a "GET" request to "https://localhost:8443/hello"
    Then the request fails because the server turned the client away

    Examples:
      | kind   |
      | PKCS12 |
      | JKS    |

  Scenario: With a truststore but no demand for one, a client may come without a certificate
    Given a client certificate is trusted through the PKCS12 truststore "trust" with password "password"
    And Shadow Mock is running with configuration:
      """yaml
      tls:
        port: 8443
        truststore: '${trust}'
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/hello" }, "response": { "status": 200 } }
      """
    When requests are sent with no client certificate
    And I send a "GET" request to "https://localhost:8443/hello"
    Then the response status is 200

  Scenario Outline: HTTPS settings that cannot work are refused at start-up
    Given the configuration file contains:
      """yaml
      <settings>
      """
    When Shadow Mock starts
    Then start-up fails with an error mentioning "<mentions>"

    Examples:
      | settings                                                   | mentions                       |
      | tls: { port: 70000 }                                        | between 0 and 65535            |
      | tls: { disableHttp: true }                                  | needs tls.port                 |
      | tls: { port: 8443, certFile: c.pem }                        | go together                    |
      | tls: { port: 8443, certFile: c.pem, keyFile: k.pem, keystore: k.p12 } | use one              |
      | tls: { port: 8443, keystore: k, keystoreType: BKS }         | must be JKS or PKCS12          |
      | tls: { port: 8443, truststore: t, truststoreType: BKS }     | must be JKS or PKCS12          |
      | tls: { port: 8443, requireClientCert: true }                | needs tls.truststore           |
      | tls: { adminRequireHttps: true }                            | needs tls.port                 |
      | tls: { keystore: k.p12 }                                    | need tls.port                  |
