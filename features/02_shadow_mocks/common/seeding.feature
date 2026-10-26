@key-based @url-based @P2 @FR-8.7
Feature: Seeding sessions with mappings at creation

  Background:
    Given Shadow Mock is running

  Scenario Outline: Mappings supplied in the create payload
    When I call admin "POST" "/sessions/<kind>" with:
      """json
      { "mappings": [
          { "request": { "urlPath": "/seed-1" }, "response": { "status": 200, "body": "one" } },
          { "request": { "urlPath": "/seed-2" }, "response": { "status": 200, "body": "two" } } ] }
      """
    Then the response status is 201
    And the created session has 2 mappings

    Examples:
      | kind      |
      | key-based |
      | url-based |

  Scenario: Invalid seed mappings fail the whole create
    When I call admin "POST" "/sessions/key-based" with:
      """json
      { "mappings": [ { "request": { "urlPath": "/ok" }, "response": { "status": 200 } },
                      { "request": { "url": "/a", "urlPath": "/a" }, "response": { "status": 200 } } ] }
      """
    Then the response status is 422
    And no session was created

  @standalone
  Scenario: Seed from a named folder of mappings
    Given the folder "mapping-sets/declined-payments" contains 3 mapping files
    When I call admin "POST" "/sessions/key-based" with:
      """json
      { "seedFrom": { "mappingSet": "declined-payments" } }
      """
    Then the created session has 3 mappings

  @saas
  Scenario: Seed from a named mapping set saved in the management app
    Given the org "acme" has a saved mapping set "declined-payments" with 3 mappings
    When I call admin "POST" "/sessions/url-based" with:
      """json
      { "seedFrom": { "mappingSet": "declined-payments" } }
      """
    Then the created session has 3 mappings

  Scenario: Seeding copies mappings, so later changes to the source do not affect the session
    Given a key-based shadow mock "S1" seeded from mapping set "base"
    When the mapping set "base" is changed
    Then the mappings in "S1" are unchanged
