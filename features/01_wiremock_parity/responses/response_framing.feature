@wiremock @P3 @standalone @FR-10.10
Feature: What a response says about itself, and how request bodies arrive
  A reply carries the reason phrase the mapping asks for and the headers that name the mapping that
  served it, and a request body sent with Content-Encoding: gzip is read as text. Every expectation
  below was checked against a real WireMock 3.13.2.

  Background:
    Given Shadow Mock is running

  Scenario: statusMessage is the reason phrase
    Given the global mapping:
      """json
      { "request": { "urlPath": "/sm" }, "response": { "status": 200, "statusMessage": "All Good", "body": "x" } }
      """
    When I send a "GET" request to "/sm"
    Then the response status is 200
    And the response status message is "All Good"
    And the response body equals 'x'

  Scenario: A reason phrase on a status that Go would name differently, with headers and no body
    Given the global mapping:
      """json
      { "request": { "urlPath": "/nope" },
        "response": { "status": 404, "statusMessage": "Nope", "headers": { "X-A": "b" } } }
      """
    When I send a "GET" request to "/nope"
    Then the response status is 404
    And the response status message is "Nope"
    And the response header "X-A" is "b"

  Scenario: A line break in the reason phrase cannot start a header
    Given the global mapping:
      """json
      { "request": { "urlPath": "/inject" },
        "response": { "status": 200, "statusMessage": "Fine\r\nX-Injected: yes" } }
      """
    When I send a "GET" request to "/inject"
    Then the response status is 200
    And the response header "X-Injected" is absent

  Scenario: A reply names the mapping that served it
    Given the global mapping:
      """json
      { "id": "aaaaaaaa-0000-4000-8000-000000000001", "name": "claims lookup",
        "request": { "urlPath": "/named" }, "response": { "status": 200 } }
      """
    And the global mapping:
      """json
      { "id": "bbbbbbbb-0000-4000-8000-000000000002", "request": { "urlPath": "/unnamed" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/named"
    Then the response header "Matched-Stub-Id" is "aaaaaaaa-0000-4000-8000-000000000001"
    And the response header "Matched-Stub-Name" is "claims lookup"
    When I send a "GET" request to "/unnamed"
    Then the response header "Matched-Stub-Id" is "bbbbbbbb-0000-4000-8000-000000000002"
    And the response header "Matched-Stub-Name" is absent

  Scenario: A request nothing matched has neither header
    When I send a "GET" request to "/nothing"
    Then the response status is 404
    And the response header "Matched-Stub-Id" is absent

  Scenario: The journal keeps the headers of the reply
    Given the global mapping:
      """json
      { "id": "aaaaaaaa-0000-4000-8000-000000000001", "name": "claims lookup",
        "request": { "urlPath": "/named" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/named"
    And I call admin "GET" "/requests"
    Then the response JSON at "$.requests[0].response.headers.Matched-Stub-Id" is "aaaaaaaa-0000-4000-8000-000000000001"
    And the response JSON at "$.requests[0].response.headers.Matched-Stub-Name" is "claims lookup"

  Scenario: A request body sent with gzip is matched, and journalled, as text
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/echo", "bodyPatterns": [ { "equalTo": "hello gz" } ] },
        "response": { "status": 200, "body": "matched" } }
      """
    When I send a "POST" request to "/echo" with a gzip body:
      """
      hello gz
      """
    Then the response status is 200
    And the response body equals 'matched'
    When I call admin "GET" "/requests"
    Then the response JSON at "$.requests[0].request.body" is "hello gz"
    And the response JSON at "$.requests[0].request.headers.Content-Encoding" is "gzip"

  Scenario: A templated response reads the decoded request body
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/tpl" },
        "response": { "status": 200, "body": "got {{request.body}}", "transformers": ["response-template"] } }
      """
    When I send a "POST" request to "/tpl" with a gzip body:
      """
      abc
      """
    Then the response body equals 'got abc'
