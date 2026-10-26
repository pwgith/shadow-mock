@key-based @url-based @P2 @FR-1.2
Feature: Session-scoped response files
  bodyFileName is resolved in the session's own files first, then in the
  global __files.

  Background:
    Given Shadow Mock is running
    And the file "__files/claim.json":
      """json
      { "source": "global" }
      """
    And a key-based shadow mock "S1"
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/claim" }, "response": { "status": 200, "bodyFileName": "claim.json" } }
      """

  Scenario: Without a session file the global file is used
    When I send a "GET" request to "/claim" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response JSON at "$.source" is "global"

  Scenario: A session file overrides the global file for that session only
    When I call admin "PUT" "/sessions/${S1.id}/files/claim.json" with:
      """json
      { "source": "session" }
      """
    And I send a "GET" request to "/claim" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response JSON at "$.source" is "session"

  Scenario: Session files are deleted with the session
    Given I call admin "PUT" "/sessions/${S1.id}/files/claim.json" with:
      """json
      { "source": "session" }
      """
    When I call admin "DELETE" "/sessions/${S1.id}"
    Then the blob store holds no files for "S1"
