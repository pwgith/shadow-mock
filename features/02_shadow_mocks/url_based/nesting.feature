@url-based @key-based @P2 @FR-8.5
Feature: Key-based shadow mocks nested inside a URL-based shadow mock
  A test can create a key-based shadow mock inside a URL-based one, giving the
  stack key-based, then URL-based, then global.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPathPattern": "/.*" }, "response": { "status": 200, "body": "global" } }
      """
    And a URL-based shadow mock "U1"
    And the mapping in "U1":
      """json
      { "request": { "urlPathPattern": "/(a|b)" }, "response": { "status": 200, "body": "url-based" } }
      """
    And a key-based shadow mock "K1" with:
      """json
      { "parentId": "${U1.id}" }
      """
    And the mapping in "K1":
      """json
      { "request": { "urlPath": "/a" }, "response": { "status": 200, "body": "nested" } }
      """

  Scenario Outline: The three-layer stack
    When I send a "GET" request to "<path>" via "U1"<key>
    Then the response body is "<body>"

    Examples:
      | path | key                                                     | body      |
      | /a   |  with header "X-Shadow-Mock-Key" set to "${K1.key}"     | nested    |
      | /b   |  with header "X-Shadow-Mock-Key" set to "${K1.key}"     | url-based |
      | /c   |  with header "X-Shadow-Mock-Key" set to "${K1.key}"     | global    |
      | /a   |                                                         | url-based |

  Scenario: A nested key is only recognised under its parent URL-based mock
    When I send a "GET" request to "/a" with header "X-Shadow-Mock-Key" set to "${K1.key}"
    Then the response body is "global"

  Scenario: The nested session starts its call counters at zero
    Given the global mapping:
      """json
      { "request": { "urlPath": "/n" }, "response": { "status": 200 }, "callResponses": [ { "calls": "1", "response": { "status": 201 } } ] }
      """
    And I send a "GET" request to "/n" via "U1"
    And a key-based shadow mock "K2" with:
      """json
      { "parentId": "${U1.id}" }
      """
    When I send a "GET" request to "/n" via "U1" with header "X-Shadow-Mock-Key" set to "${K2.key}"
    Then the response status is 201

  Scenario: Deleting the parent deletes its nested sessions
    When I call admin "DELETE" "/sessions/${U1.id}"
    Then admin "GET" "/sessions/${K1.id}" returns status 404

  Scenario: A nested session cannot outlive its parent
    When I call admin "POST" "/sessions/key-based" with a TTL beyond the expiry of "U1" and parent "${U1.id}"
    Then the session's expiresAt equals the expiry of "U1"
