@key-based @P1 @FR-7.4
Feature: Session detection through designated header, query parameter and cookie
  Stages 1 to 3 of detection: an exact lookup of the session key in the
  designated header, then the query parameter, then the cookie.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPath": "/pay" }, "response": { "status": 200, "body": "global" } }
      """
    And a key-based shadow mock "S1"
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/pay" }, "response": { "status": 200, "body": "shadow" } }
      """

  Scenario Outline: The key is found in each designated location
    When I send a request:
      | method | path   | <location>  |
      | GET    | <path> | ${S1.key}   |
    Then the response body is "shadow"

    Examples:
      | location                  | path |
      | header:X-Shadow-Mock-Key  | /pay |
      | cookie:shadow_mock_key    | /pay |
      | query:shadow_mock_key     | /pay |

  Scenario: Without a key the request uses the global session
    When I send a "GET" request to "/pay"
    Then the response body is "global"

  Scenario: An unknown or expired key falls back to the global session
    When I send a "GET" request to "/pay" with header "X-Shadow-Mock-Key" set to "smk_00000000000000000000000000"
    Then the response body is "global"

  Scenario: The designated header wins over the query parameter
    Given a key-based shadow mock "S2"
    And the mapping in "S2":
      """json
      { "request": { "urlPath": "/pay" }, "response": { "status": 200, "body": "shadow-2" } }
      """
    When I send a request:
      | method | path                          | header:X-Shadow-Mock-Key |
      | GET    | /pay?shadow_mock_key=${S2.key} | ${S1.key}                |
    Then the response body is "shadow"

  Scenario: The designated query parameter does not break query matching
    Given the mapping in "S1":
      """json
      { "request": { "urlPath": "/q", "queryParameters": { "page": { "equalTo": "1" } } }, "response": { "status": 200, "body": "page1" } }
      """
    When I send a "GET" request to "/q?page=1&shadow_mock_key=${S1.key}"
    Then the response body is "page1"

  @standalone
  Scenario: Designated names are configurable
    Given Shadow Mock is running with configuration:
      """yaml
      sessions: { detect: { headers: [X-Test-Session, X-Correlation-Session], queryParams: [tsid] } }
      """
    And a key-based shadow mock "S9"
    And the mapping in "S9":
      """json
      { "request": { "urlPath": "/c" }, "response": { "status": 200, "body": "configured" } }
      """
    When I send a "GET" request to "/c" with header "X-Correlation-Session" set to "${S9.key}"
    Then the response body is "configured"
    When I send a "GET" request to "/c?tsid=${S9.key}"
    Then the response body is "configured"

  # Needs the detection metrics (P4).
  @wip
  Scenario: Detection is skipped entirely when there are no active sessions
    Given I call admin "DELETE" "/sessions/${S1.id}"
    And there are no active sessions
    When I send a "GET" request to "/pay" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response body is "global"
    And the detection metrics show 0 scans for that request
