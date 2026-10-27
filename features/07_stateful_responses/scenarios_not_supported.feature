@P0 @FR-4.4 @FR-1.3
Feature: WireMock scenarios are not supported
  Shadow Mock deliberately does not implement WireMock scenarios. Mappings
  that use them are rejected with a clear message, never loaded with the
  scenario fields silently ignored.

  Background:
    Given Shadow Mock is running

  Scenario Outline: A mapping with any scenario field is rejected through the admin API
    When I create a session mapping:
      """json
      { "request": { "urlPath": "/s" }, "response": { "status": 200 }, <fields> }
      """
    Then the response status is 422
    And the response JSON at "$.error" is "scenarios_not_supported"
    And the response JSON at "$.message" contains "callResponses"

    Examples:
      | fields                                                   |
      | "scenarioName": "flow"                                   |
      | "scenarioName": "flow", "requiredScenarioState": "Started" |
      | "newScenarioState": "Done"                                |

  @standalone
  Scenario: Mapping files with scenario fields are reported and skipped
    Given the global mappings file "mappings/stateful.json":
      """json
      { "scenarioName": "flow", "requiredScenarioState": "Started", "newScenarioState": "Done",
        "request": { "urlPath": "/stateful" }, "response": { "status": 200 } }
      """
    And the global mappings file "mappings/plain.json":
      """json
      { "request": { "urlPath": "/plain" }, "response": { "status": 200 } }
      """
    When Shadow Mock is started with root "./wiremock"
    Then the log contains an error mentioning "mappings/stateful.json" and "scenarios_not_supported"
    And a "GET" request to "/stateful" returns status 404
    And a "GET" request to "/plain" returns status 200

  Scenario: An import containing a scenario mapping is rejected as a whole
    When I import into a key-based shadow mock:
      """json
      { "mappings": [
          { "request": { "urlPath": "/ok" }, "response": { "status": 200 } },
          { "scenarioName": "flow", "request": { "urlPath": "/no" }, "response": { "status": 200 } } ] }
      """
    Then the response status is 422
    And no mappings were imported

  Scenario Outline: Scenario admin endpoints are not available
    When I call admin "<method>" "<path>"
    Then the response status is 404
    And the response JSON at "$.error" is "not_supported"

    Examples:
      | method | path                          |
      | GET    | /scenarios                    |
      | POST   | /scenarios/reset              |
      | PUT    | /scenarios/flow/state         |

  @P3
  Scenario: Recording offers call-count responses instead of scenarios
    Given the upstream stub server "jobs" returns "PENDING" then "COMPLETE" for "GET /jobs/1"
    And a URL-based shadow mock "U1"
    When I call admin "POST" "/sessions/${U1.id}/recordings/start" with:
      """json
      { "targetBaseUrl": "${upstream.jobs}", "repeatsAsCallResponses": true }
      """
    And I send 2 "GET" requests to "/jobs/1" via "U1"
    And I call admin "POST" "/sessions/${U1.id}/recordings/stop"
    Then the response JSON at "$.mappings" has 1 items
    And the response JSON at "$.mappings[0].callResponses[0].calls" is "1"
    And the response JSON at "$.mappings[0].scenarioName" is absent
