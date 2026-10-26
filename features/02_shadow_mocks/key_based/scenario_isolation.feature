@key-based @P1 @FR-7.6 @FR-4.2
Feature: Scenario state isolation
  Each session takes its own copy of every global scenario's state when it is
  created. From then on it reads and writes only its copy, including when a
  global mapping serves the request.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "scenarioName": "order", "requiredScenarioState": "Started", "newScenarioState": "Placed",
        "request": { "method": "POST", "urlPath": "/orders" }, "response": { "status": 201 } }
      """
    And the global mapping:
      """json
      { "scenarioName": "order", "requiredScenarioState": "Placed",
        "request": { "method": "GET", "urlPath": "/orders/1" }, "response": { "status": 200, "body": "placed" } }
      """
    And the global mapping:
      """json
      { "scenarioName": "order", "requiredScenarioState": "Started",
        "request": { "method": "GET", "urlPath": "/orders/1" }, "response": { "status": 404 } }
      """

  Scenario: A session advancing a global scenario does not affect the global session
    Given a key-based shadow mock "S1"
    When I send a "POST" request to "/orders" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    And I send a "GET" request to "/orders/1" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response body is "placed"
    When I send a "GET" request to "/orders/1"
    Then the response status is 404

  Scenario: Two sessions advance the same scenario independently
    Given a key-based shadow mock "S1"
    And a key-based shadow mock "S2"
    When I send a "POST" request to "/orders" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then a "GET" request to "/orders/1" with key "${S1.key}" returns status 200
    And a "GET" request to "/orders/1" with key "${S2.key}" returns status 404

  Scenario: The session copy is taken at creation and ignores later global changes
    Given I send a "POST" request to "/orders"
    And a key-based shadow mock "S1"
    When I call admin "POST" "/scenarios/reset"
    Then a "GET" request to "/orders/1" returns status 404
    And a "GET" request to "/orders/1" with key "${S1.key}" returns status 200

  Scenario: Resetting scenarios in a session sets them to Started, not to the global state
    Given I send a "POST" request to "/orders"
    And a key-based shadow mock "S1"
    When I call admin "POST" "/sessions/${S1.id}/scenarios/reset"
    Then a "GET" request to "/orders/1" with key "${S1.key}" returns status 404
    And a "GET" request to "/orders/1" returns status 200

  Scenario: A scenario added to the global session after the fork starts at Started in the session
    Given a key-based shadow mock "S1"
    And the global mapping:
      """json
      { "scenarioName": "late", "requiredScenarioState": "Started", "request": { "urlPath": "/late" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/late" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 200

  @multi-instance @saas
  Scenario: Session scenario state is shared across instances
    Given Shadow Mock is running with 2 instances
    And a key-based shadow mock "S1"
    When I send a "POST" request to "/orders" through instance 1 with header "X-Shadow-Mock-Key" set to "${S1.key}"
    And I send a "GET" request to "/orders/1" through instance 2 with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response body is "placed"

  @nfr
  Scenario: Concurrent sessions never see each other's transitions
    Given 50 key-based shadow mocks
    When each session concurrently sends "POST /orders" and then "GET /orders/1" 20 times
    Then every session observes only its own transitions
