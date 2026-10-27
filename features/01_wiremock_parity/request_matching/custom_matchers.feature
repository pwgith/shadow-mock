@standalone @P3 @FR-2.7
Feature: Custom request matchers through the Go extension interface
  Java extensions are replaced by Go plug-ins registered at build time
  through pkg/extension.

  Background:
    Given Shadow Mock is built with the custom matcher "is-even-id" that matches when query parameter "id" is even
    And Shadow Mock is running

  @p3-matchers
  Scenario: A mapping can require a custom matcher by name
    Given the global mapping:
      """json
      { "request": { "urlPath": "/even", "customMatcher": { "name": "is-even-id", "parameters": {} } },
        "response": { "status": 200 } }
      """
    When I send a "GET" request to "/even?id=4"
    Then the response status is 200
    When I send a "GET" request to "/even?id=5"
    Then the response status is 404

  @p3-matchers
  Scenario: An unknown custom matcher is rejected when the mapping is created
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/x", "customMatcher": { "name": "nope" } }, "response": { "status": 200 } }
      """
    Then the response status is 422
    And the response JSON at "$.errors[0].detail" contains "unknown custom matcher"
