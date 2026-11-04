@counters
Feature: The console's call counters
  Mappings that answer differently by call number keep a counter. The Counters section shows, sets and resets them.

  Background:
    Given the global mapping:
      """json
      { "id": "eeeeeeee-0000-0000-0000-0000000000c1", "request": { "urlPath": "/k" }, "response": { "status": 200 },
        "callResponses": [ { "calls": "10", "response": { "status": 500 } } ] }
      """

  Scenario: No counters before the first call
    Given I open the console
    When I open the "Counters" tab
    Then the console says there are no call counters

  Scenario: A counter shows the calls so far
    Given I send 2 "GET" requests to "/k"
    And I open the console
    When I open the "Counters" tab
    Then the counter for "eeeeeeee-0000-0000-0000-0000000000c1" shows 2 calls

  Scenario: Setting a counter changes the next response
    Given I send 2 "GET" requests to "/k"
    And I open the console
    And I open the "Counters" tab
    When I set the counter for "eeeeeeee-0000-0000-0000-0000000000c1" to 9
    And I send a "GET" request to "/k"
    Then the mock answers with status 500

  Scenario: Resetting one counter
    Given I send 3 "GET" requests to "/k"
    And I open the console
    And I open the "Counters" tab
    When I reset the counter for "eeeeeeee-0000-0000-0000-0000000000c1"
    Then the counter for "eeeeeeee-0000-0000-0000-0000000000c1" is back to zero

  Scenario: Resetting every counter asks first
    Given I send 3 "GET" requests to "/k"
    And I open the console
    And I open the "Counters" tab
    When I reset all counters
    Then I was asked "Reset every call counter in this scope to zero?"
    And the counter for "eeeeeeee-0000-0000-0000-0000000000c1" is back to zero

  Scenario: Declining the reset leaves the counters alone
    Given I send 3 "GET" requests to "/k"
    And I will decline the confirmation
    And I open the console
    And I open the "Counters" tab
    When I reset all counters
    Then the counter for "eeeeeeee-0000-0000-0000-0000000000c1" shows 3 calls

  Scenario: A shadow mock keeps counters of its own
    Given a key-based shadow mock "S1"
    And I send 2 "GET" requests to "/k"
    And I send a "GET" request to "/k" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    And I open the console
    And I open the "Counters" tab
    When I choose the "S1 (key-based)" scope
    Then the counter for "eeeeeeee-0000-0000-0000-0000000000c1" shows 1 calls
