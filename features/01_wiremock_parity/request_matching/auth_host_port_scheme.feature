@wiremock @P0 @FR-2.3
Feature: Basic auth, host, port and scheme matching

  Background:
    Given Shadow Mock is running

  Scenario Outline: basicAuthCredentials
    Given the global mapping:
      """json
      { "request": { "urlPath": "/secure", "basicAuthCredentials": { "username": "svc", "password": "s3cret" } },
        "response": { "status": 200 } }
      """
    When I send a "GET" request to "/secure" with basic auth "<user>" / "<password>"
    Then the response status is <status>

    Examples:
      | user | password | status |
      | svc  | s3cret   | 200    |
      | svc  | wrong    | 404    |

  Scenario: Host matching uses the Host header
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h", "host": { "equalTo": "payments.internal" } }, "response": { "status": 200 } }
      """
    When I send a request:
      | method | path | header:Host       |
      | GET    | /h   | payments.internal |
    Then the response status is 200

  @standalone
  Scenario: Port and scheme matching
    Given Shadow Mock is listening on http port 8080 and https port 8443
    And the global mapping:
      """json
      { "request": { "urlPath": "/s", "scheme": "https", "port": 8443 }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "https://localhost:8443/s"
    Then the response status is 200
    When I send a "GET" request to "http://localhost:8080/s"
    Then the response status is 404
