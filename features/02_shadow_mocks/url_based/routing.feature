@url-based @P2 @FR-8.2 @FR-8.6
Feature: Routing through the URL insert
  Requests to {mock base}/d/<id>/... are routed to the URL-based shadow mock.
  The prefix is stripped before matching, so mappings are written exactly as
  for the real dependency.

  Background:
    Given Shadow Mock is running
    And a URL-based shadow mock "U1"
    And the mapping in "U1":
      """json
      { "request": { "method": "GET", "urlPath": "/claims/42" }, "response": { "status": 200, "body": "dedicated" } }
      """

  Scenario: The prefix is stripped before matching
    When I send a "GET" request to "/claims/42" via "U1"
    Then the response body is "dedicated"

  Scenario: Query strings survive the prefix strip
    Given the mapping in "U1":
      """json
      { "request": { "urlPath": "/search", "queryParameters": { "q": { "equalTo": "x" } } }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/search?q=x" via "U1"
    Then the response status is 200

  Scenario: Templating sees the stripped path
    Given the mapping in "U1":
      """json
      { "request": { "urlPath": "/whoami" }, "response": { "status": 200, "body": "{{request.path}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/whoami" via "U1"
    Then the response body is "/whoami"

  Scenario: An unknown URL-based mock id returns 404 and never falls back to the global session
    Given the global mapping:
      """json
      { "request": { "urlPath": "/claims/42" }, "response": { "status": 200, "body": "global" } }
      """
    When I send a "GET" request to "{mock base}/d/aaaaaaaaaaaaaaaa/claims/42"
    Then the response status is 404
    And the response JSON at "$.error" is "unknown_url_based_mock"

  Scenario: An expired URL-based mock id returns 404
    Given the test clock advances by "25h"
    When I send a "GET" request to "/claims/42" via "U1"
    Then the response status is 404

  Scenario: The URL-based base URL works with an HTTP client configured with a base path
    Given an HTTP client configured with base URL "${U1.baseUrl}"
    When the client calls "GET claims/42" relative to its base URL
    Then the response body is "dedicated"
