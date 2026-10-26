@wiremock @P1 @FR-4.1 @FR-4.2
Feature: Stateful scenarios in the global session
  Named state machines that start in "Started". Mappings can require a state
  and move the scenario to a new state.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "scenarioName": "claim", "requiredScenarioState": "Started", "newScenarioState": "Submitted",
        "request": { "method": "POST", "urlPath": "/claims" }, "response": { "status": 201 } }
      """
    And the global mapping:
      """json
      { "scenarioName": "claim", "requiredScenarioState": "Started",
        "request": { "method": "GET", "urlPath": "/claims/1" }, "response": { "status": 404 } }
      """
    And the global mapping:
      """json
      { "scenarioName": "claim", "requiredScenarioState": "Submitted",
        "request": { "method": "GET", "urlPath": "/claims/1" }, "response": { "status": 200, "body": "submitted" } }
      """

  Scenario: The scenario moves through its states
    When I send a "GET" request to "/claims/1"
    Then the response status is 404
    When I send a "POST" request to "/claims"
    And I send a "GET" request to "/claims/1"
    Then the response body is "submitted"

  Scenario: List scenarios and their current state
    When I send a "POST" request to "/claims"
    And I call admin "GET" "/scenarios"
    Then the response JSON at "$.scenarios[?(@.name=='claim')].state" is "Submitted"
    And the response JSON at "$.scenarios[?(@.name=='claim')].possibleStates" contains "Started"

  Scenario: Reset all scenarios
    Given I send a "POST" request to "/claims"
    When I call admin "POST" "/scenarios/reset"
    And I send a "GET" request to "/claims/1"
    Then the response status is 404

  Scenario: Set a single scenario's state directly
    When I call admin "PUT" "/scenarios/claim/state" with:
      """json
      { "state": "Submitted" }
      """
    And I send a "GET" request to "/claims/1"
    Then the response body is "submitted"

  Scenario: Reset a single scenario
    Given I send a "POST" request to "/claims"
    When I call admin "PUT" "/scenarios/claim/state" with:
      """json
      {}
      """
    Then admin "GET" "/scenarios" shows scenario "claim" in state "Started"

  Scenario: Mappings without a required state match in any state
    Given the global mapping:
      """json
      { "scenarioName": "claim", "request": { "urlPath": "/claims/any" }, "response": { "status": 200 } }
      """
    And I send a "POST" request to "/claims"
    When I send a "GET" request to "/claims/any"
    Then the response status is 200
