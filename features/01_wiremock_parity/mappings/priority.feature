@wiremock @P0 @FR-2.6
Feature: Mapping priority within a layer
  When several mappings in the same layer match a request, the one with the
  lowest priority number wins. Ties go to the most recently added mapping.
  A mapping with no priority uses the WireMock default of 5.

  Background:
    Given Shadow Mock is running

  Scenario: The lower priority number wins
    Given the global mapping:
      """json
      { "priority": 10, "request": { "urlPattern": "/claims/.*" }, "response": { "status": 200, "body": "generic" } }
      """
    And the global mapping:
      """json
      { "priority": 1, "request": { "url": "/claims/42" }, "response": { "status": 200, "body": "specific" } }
      """
    When I send a "GET" request to "/claims/42"
    Then the response body is "specific"

  Scenario: With equal priority the most recently added mapping wins
    Given the global mapping:
      """json
      { "request": { "url": "/dup" }, "response": { "status": 200, "body": "first" } }
      """
    And the global mapping:
      """json
      { "request": { "url": "/dup" }, "response": { "status": 200, "body": "second" } }
      """
    When I send a "GET" request to "/dup"
    Then the response body is "second"

  Scenario Outline: A mapping without a priority behaves as priority 5
    Given the global mapping:
      """json
      { "request": { "url": "/p" }, "response": { "status": 200, "body": "default" } }
      """
    And the global mapping:
      """json
      { "priority": <other>, "request": { "url": "/p" }, "response": { "status": 200, "body": "explicit" } }
      """
    When I send a "GET" request to "/p"
    Then the response body is "<winner>"

    Examples:
      | other | winner   |
      | 4     | explicit |
      | 6     | default  |
