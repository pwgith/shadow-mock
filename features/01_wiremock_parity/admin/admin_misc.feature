@wiremock @FR-6.3 @FR-6.5
Feature: Other WireMock admin endpoints: files, settings, reset, health and shutdown

  Background:
    Given Shadow Mock is running

  @standalone @P0
  Scenario: Manage __files through the admin API
    When I call admin "PUT" "/files/greeting.txt" with:
      """
      hello
      """
    Then admin "GET" "/files" lists "greeting.txt"
    And admin "GET" "/files/greeting.txt" returns body "hello"
    When I call admin "DELETE" "/files/greeting.txt"
    Then admin "GET" "/files" does not list "greeting.txt"

  @standalone @P0
  Scenario: Full reset clears API mappings, the journal and scenarios
    Given the global mapping:
      """json
      { "request": { "urlPath": "/r" }, "response": { "status": 200 } }
      """
    And I send a "GET" request to "/r"
    When I call admin "POST" "/reset"
    Then admin "GET" "/requests" lists 0 requests
    And a "GET" request to "/r" returns status 404

  @standalone @P3
  Scenario: Read and update global settings
    When I call admin "POST" "/settings" with:
      """json
      { "fixedDelay": 0, "extended": { "team": "claims" } }
      """
    Then admin "GET" "/settings" returns JSON at "$.settings.extended.team" equal to "claims"

  @P0
  Scenario: Health endpoint
    When I call admin "GET" "/health"
    Then the response status is 200
    And the response JSON at "$.status" is "healthy"

  @standalone @P0
  Scenario: Shutdown stops the server in standalone mode
    When I call admin "POST" "/shutdown"
    Then the response status is 200
    And within 5 s the server has stopped

  @saas @P5
  Scenario Outline: Endpoints that are disabled or restricted in SaaS
    Given I am "alice" in org "acme" with role "org:admin"
    When I call admin "<method>" "<path>"
    Then the response status is <status>
    And the response JSON at "$.error" is "<error>"

    Examples:
      | method | path             | status | error                            |
      | POST   | /shutdown        | 404    | not_available_in_saas            |
      | POST   | /mappings/save   | 404    | not_available_in_saas            |
      | POST   | /reset           | 403    | global_writes_via_management_app |
      | PUT    | /files/x.json    | 403    | global_writes_via_management_app |
      | POST   | /settings        | 403    | global_writes_via_management_app |
