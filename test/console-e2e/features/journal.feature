@journal
Feature: The console's journal
  A person debugging a test opens the console to see what the mock was asked and why a request was not served.

  Background:
    Given the global mapping:
      """json
      { "name": "Claim by id", "request": { "method": "GET", "urlPath": "/claims/1", "headers": { "Accept": { "equalTo": "application/json" } } },
        "response": { "status": 200, "body": "claim one" } }
      """

  Scenario: Matched and unmatched requests are listed and marked
    Given I send a "GET" request to "/claims/1" with header "Accept" set to "application/json"
    And I send a "GET" request to "/claims/1"
    When I open the console
    Then the journal lists "GET /claims/1"
    And the journal entry "GET /claims/1" is marked "matched"
    And the journal entry "GET /claims/1" is marked "unmatched"

  Scenario: An empty journal says so
    When I open the console and see no requests yet
    Then the page has no script errors

  Scenario: Showing only unmatched requests
    Given I send a "GET" request to "/claims/1" with header "Accept" set to "application/json"
    And I send a "GET" request to "/nowhere"
    And I open the console
    When I show only unmatched requests
    Then the journal lists "GET /nowhere"
    And the journal does not list "GET /claims/1"

  Scenario: Selecting an unmatched request shows the closest mappings and what each missed
    Given I send a "GET" request to "/claims/1"
    And I open the console
    When I select the journal entry "GET /claims/1"
    Then the journal detail shows "Request"
    And the journal detail shows "Response"
    And the journal detail shows "Closest mappings"
    And the journal detail shows "Claim by id"
    And the journal detail shows "Missed on: Header [Accept]"

  Scenario: Selecting a matched request shows which layer served it
    Given I send a "GET" request to "/claims/1" with header "Accept" set to "application/json"
    And I open the console
    When I select the journal entry "GET /claims/1"
    Then the journal detail shows "Served by"
    And the journal detail shows "global layer, mapping Claim by id"
    And the journal detail does not show "Closest mappings"

  Scenario: A request that carries markup is shown as text and never runs
    Given I send a "GET" request to "/claims/9" with header "X-Evil" set to "<img src=x onerror=window.pwned=1>"
    And I open the console
    When I select the journal entry "GET /claims/9"
    Then the journal detail shows "<img src=x onerror=window.pwned=1>"
    And the journal detail holds no image or script element
    And the page has no script errors

  Scenario: Refreshing picks up new requests
    Given I open the console and see no requests yet
    And I send a "GET" request to "/later"
    When I refresh the journal
    Then the journal lists "GET /later"
