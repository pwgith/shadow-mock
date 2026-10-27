@standalone @central-server @P3 @NFR-5.2 @FR-9.6b
Feature: Separate admin listener and IP allowlist
  A shared server can keep the admin API on its own port and accept mock
  traffic only from known networks.

  Scenario: Admin API on a separate listener
    Given Shadow Mock is running with configuration:
      """yaml
      listen: { mock: ":8080", admin: ":9090" }
      """
    When I send a "GET" request to "http://localhost:8080/__admin/mappings"
    Then the response status is 404
    When I send a "GET" request to "http://localhost:9090/__admin/mappings"
    Then the response status is 200

  Scenario: Mock traffic is served on the mock listener only
    Given Shadow Mock is running with configuration:
      """yaml
      listen: { mock: ":8080", admin: ":9090" }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200, "body": "mock" } }
      """
    When I send a "GET" request to "http://localhost:8080/x"
    Then the response body is "mock"
    When I send a "GET" request to "http://localhost:9090/x"
    Then the response status is 404

  Scenario: Admin calls work through the separate listener
    Given Shadow Mock is running with configuration:
      """yaml
      listen: { mock: ":8080", admin: ":9090" }
      """
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/made" }, "response": { "status": 200, "body": "made" } }
      """
    Then the response status is 201
    And a "GET" request to "http://localhost:8080/made" returns body "made"

  Scenario: Health checks are available on both listeners
    Given Shadow Mock is running with configuration:
      """yaml
      listen: { mock: ":8080", admin: ":9090" }
      """
    Then a "GET" request to "http://localhost:8080/__health" returns status 200
    And a "GET" request to "http://localhost:9090/__health" returns status 200
    And a "GET" request to "http://localhost:8080/__ready" returns status 200
    And a "GET" request to "http://localhost:9090/__ready" returns status 200

  Scenario: Metrics are served on the admin listener and hidden on the mock listener
    Given Shadow Mock is running with configuration:
      """yaml
      listen: { mock: ":8080", admin: ":9090" }
      """
    Then a "GET" request to "http://localhost:8080/__metrics" returns status 404
    And a "GET" request to "http://localhost:9090/__metrics" returns status 200

  Scenario: Restrict mock traffic on a shared network with a local IP allowlist
    Given Shadow Mock is running with configuration:
      """yaml
      tenancy: { local: { ipAllowlist: ["10.20.0.0/16"] } }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/x" from IP "10.20.3.4"
    Then the response status is 200
    When I send a "GET" request to "/x" from IP "192.168.1.9"
    Then the response status is 403
    And the response JSON at "$.error" is "ip_not_allowed"

  Scenario Outline: The allowlist accepts any listed network and nothing outside them
    Given Shadow Mock is running with configuration:
      """yaml
      tenancy: { local: { ipAllowlist: ["10.20.0.0/16", "192.168.5.0/24", "2001:db8::/32"] } }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/x" from IP "<ip>"
    Then the response status is <status>

    Examples:
      | ip            | status |
      | 10.20.0.1     | 200    |
      | 10.20.255.254 | 200    |
      | 10.21.0.1     | 403    |
      | 192.168.5.200 | 200    |
      | 192.168.6.1   | 403    |
      | 2001:db8::7   | 200    |
      | 2001:db9::7   | 403    |

  Scenario: The allowlist applies to shadow mock traffic too
    Given Shadow Mock is running with configuration:
      """yaml
      tenancy: { local: { ipAllowlist: ["10.20.0.0/16"] } }
      """
    And a key-based shadow mock "S1"
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200, "body": "session" } }
      """
    When I send a request:
      | method | path | header:X-Forwarded-For | header:X-Shadow-Mock-Key |
      | GET    | /x   | 10.20.3.4             | ${S1.key}               |
    Then the response body is "session"
    When I send a request:
      | method | path | header:X-Forwarded-For | header:X-Shadow-Mock-Key |
      | GET    | /x   | 192.168.1.9           | ${S1.key}               |
    Then the response status is 403

  Scenario: The allowlist does not restrict the admin API by default
    Given Shadow Mock is running with configuration:
      """yaml
      tenancy: { local: { ipAllowlist: ["10.20.0.0/16"] } }
      """
    When I call admin "GET" "/mappings" with header "X-Forwarded-For" set to "192.168.1.9"
    Then the response status is 200
