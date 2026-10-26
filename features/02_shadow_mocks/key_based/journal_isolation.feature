@key-based @P1 @FR-7.11
Feature: Journal isolation
  A session's journal holds only the requests attributed to it. The global
  journal holds everything, tagged with the session that served it.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPathPattern": "/.*" }, "response": { "status": 200 } }
      """
    And a key-based shadow mock "S1"
    And a key-based shadow mock "S2"
    And I send a "GET" request to "/one" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    And I send a "GET" request to "/two" with header "X-Shadow-Mock-Key" set to "${S2.key}"
    And I send a "GET" request to "/three"

  Scenario: Each session sees only its own requests
    When I call admin "GET" "/sessions/${S1.id}/requests"
    Then the response JSON at "$.requests" has 1 items
    And the response JSON at "$.requests[0].request.url" is "/one"

  Scenario: The global journal sees everything, tagged by session
    When I call admin "GET" "/requests"
    Then the response JSON at "$.requests" has 3 items
    And the entry for "/two" has "shadowMock.sessionId" equal to "${S2.id}"
    And the entry for "/three" has "shadowMock.sessionId" equal to null

  Scenario: A request served by a global mapping on behalf of a session is in the session journal
    When I call admin "POST" "/sessions/${S1.id}/requests/count" with:
      """json
      { "method": "GET", "urlPath": "/one" }
      """
    Then the response JSON at "$.count" is 1

  Scenario: Clearing a session journal leaves others intact
    When I call admin "DELETE" "/sessions/${S1.id}/requests"
    Then admin "GET" "/sessions/${S2.id}/requests" lists 1 requests
