@dryrun
Feature: The console's dry run
  A dry run shows which mapping would serve a request, without serving it and without journalling it.

  Background:
    Given the global mapping:
      """json
      { "name": "Claim one", "request": { "method": "GET", "urlPath": "/claims/1" }, "response": { "status": 200, "body": "claim one" } }
      """

  Scenario: A request that would be served shows the layer, the mapping and the response
    Given I open the console
    And I open the "Dry run" tab
    When I dry-run "GET" "/claims/1"
    Then the dry run shows "Served by the global layer"
    And the dry run shows "Mapping Claim one"
    And the dry run shows "Response 200"
    And the dry run shows "claim one"
    And the journal still holds 0 requests

  Scenario: A request nothing matches shows the closest mappings
    Given I open the console
    And I open the "Dry run" tab
    When I dry-run "POST" "/claims/1"
    Then the dry run shows "Nothing matches"
    And the dry run shows "Claim one"
    And the dry run shows "Missed on: Method"

  Scenario: A dry run in a shadow mock uses that shadow mock's mappings first
    Given a key-based shadow mock "S1"
    And the mapping in "S1":
      """json
      { "name": "Claim one, session", "request": { "method": "GET", "urlPath": "/claims/1" }, "response": { "status": 202, "body": "session claim" } }
      """
    And I open the console
    And I open the "Dry run" tab
    When I dry-run "GET" "/claims/1" in the shadow mock "S1"
    Then the dry run shows "Served by the key-based layer"
    And the dry run shows "Response 202"
    And the journal still holds 0 requests
