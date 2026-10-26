@wiremock @P0 @FR-3.6
Feature: Unmatched requests
  When no mapping matches, Shadow Mock responds 404 with a diagnostic body
  in WireMock's format, showing the closest mappings.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "name": "get claim", "request": { "method": "GET", "urlPath": "/claims/1", "headers": { "Accept": { "equalTo": "application/json" } } },
        "response": { "status": 200 } }
      """

  Scenario: The 404 body explains the closest miss
    When I send a "GET" request to "/claims/1"
    Then the response status is 404
    And the response body contains "Request was not matched"
    And the response body contains "get claim"
    And the response body contains "Accept"

  Scenario: The unmatched request is recorded in the journal
    When I send a "POST" request to "/nothing-here"
    Then admin "GET" "/requests/unmatched" lists 1 request with url "/nothing-here"

  Scenario: With no mappings at all the 404 still has a body
    Given there are no global mappings
    When I send a "GET" request to "/anything"
    Then the response status is 404
    And the response body contains "No mappings"
