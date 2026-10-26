@key-based @P1 @FR-7.10
Feature: Ambiguous detection
  When a scan finds keys or tokens for more than one session, a configurable
  policy decides what happens. The journal records the ambiguity either way.

  Background:
    Given the global mapping:
      """json
      { "request": { "urlPath": "/a" }, "response": { "status": 200, "body": "global" } }
      """

  Scenario Outline: Ambiguity policies
    Given Shadow Mock is running with ambiguity policy "<policy>"
    And a key-based shadow mock "S1" with:
      """json
      { "tokens": ["CUST-AAA111"] }
      """
    And a key-based shadow mock "S2" with:
      """json
      { "tokens": ["CUST-BBB222"] }
      """
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/a" }, "response": { "status": 200, "body": "s1" } }
      """
    And the mapping in "S2":
      """json
      { "request": { "urlPath": "/a" }, "response": { "status": 200, "body": "s2" } }
      """
    When I send a "POST" request to "/a" with body:
      """json
      { "first": "CUST-AAA111", "second": "CUST-BBB222" }
      """
    Then the response status is <status>
    And the response body contains "<body>"
    And the journal entry records an ambiguity between "S1" and "S2"

    Examples:
      | policy | status | body               |
      | first  | 200    | s1                 |
      | reject | 409    | ambiguous_session  |
      | global | 200    | global             |

  Scenario: The same session matched twice is not ambiguous
    Given Shadow Mock is running with ambiguity policy "reject"
    And a key-based shadow mock "S1" with:
      """json
      { "tokens": ["CUST-AAA111"] }
      """
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/a" }, "response": { "status": 200, "body": "s1" } }
      """
    When I send a "POST" request to "/a" with body:
      """json
      { "a": "CUST-AAA111", "b": "${S1.key}" }
      """
    Then the response body is "s1"
