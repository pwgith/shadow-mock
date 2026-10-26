@key-based @url-based @P3 @FR-6.1
Feature: Recording into a session
  Recording can target a key-based or URL-based shadow mock instead of the
  global session. This is the only form of recording in SaaS mode.

  Background:
    Given Shadow Mock is running
    And the upstream stub server "fx" returns 200 with JSON '{"rate":1.1}' for "GET /rates/EURUSD"
    And a URL-based shadow mock "U1"

  Scenario: Record through a URL-based mock into that mock
    When I call admin "POST" "/sessions/${U1.id}/recordings/start" with:
      """json
      { "targetBaseUrl": "${upstream.fx}" }
      """
    And I send a "GET" request to "/rates/EURUSD" via "U1"
    And I call admin "POST" "/sessions/${U1.id}/recordings/stop"
    Then admin "GET" "/sessions/${U1.id}/mappings" lists 1 mappings
    And admin "GET" "/mappings" does not include a mapping for "/rates/EURUSD"

  Scenario: Recordings into a key-based mock only capture traffic attributed to it
    Given a key-based shadow mock "S1"
    When I call admin "POST" "/sessions/${S1.id}/recordings/start" with:
      """json
      { "targetBaseUrl": "${upstream.fx}" }
      """
    And I send a "GET" request to "/rates/EURUSD" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    And I send a "GET" request to "/rates/EURUSD"
    And I call admin "POST" "/sessions/${S1.id}/recordings/stop"
    Then the response JSON at "$.mappings" has 1 items
