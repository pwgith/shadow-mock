@wiremock @P0 @FR-3.1
Feature: Response definitions

  Background:
    Given Shadow Mock is running

  Scenario: Status, status message, headers and a text body
    Given the global mapping:
      """json
      { "request": { "urlPath": "/r" },
        "response": { "status": 418, "statusMessage": "I'm a teapot",
                      "headers": { "X-Trace": "t-1", "Content-Type": "text/plain" }, "body": "short and stout" } }
      """
    When I send a "GET" request to "/r"
    Then the response status is 418
    And the response status message is "I'm a teapot"
    And the response header "X-Trace" is "t-1"
    And the response body is "short and stout"

  Scenario: jsonBody is serialised as JSON
    Given the global mapping:
      """json
      { "request": { "urlPath": "/json" },
        "response": { "status": 200, "jsonBody": { "id": 1, "tags": ["a"] } } }
      """
    When I send a "GET" request to "/json"
    Then the response JSON at "$.tags[0]" is "a"

  Scenario: base64Body returns the decoded bytes
    Given the global mapping:
      """json
      { "request": { "urlPath": "/b64" }, "response": { "status": 200, "base64Body": "aGVsbG8=" } }
      """
    When I send a "GET" request to "/b64"
    Then the response body is "hello"

  Scenario: Multi-value response headers
    Given the global mapping:
      """json
      { "request": { "urlPath": "/mh" }, "response": { "status": 200, "headers": { "Set-Cookie": ["a=1", "b=2"] } } }
      """
    When I send a "GET" request to "/mh"
    Then the response has 2 "Set-Cookie" headers

  Scenario: bodyFileName serves a binary file with its content type
    Given the file "__files/logo.png" contains 2048 bytes of PNG data
    And the global mapping:
      """json
      { "request": { "urlPath": "/logo" }, "response": { "status": 200, "bodyFileName": "logo.png", "headers": { "Content-Type": "image/png" } } }
      """
    When I send a "GET" request to "/logo"
    Then the response body is the 2048 bytes of "__files/logo.png"

  Scenario: A missing body file results in a 500 with a clear message
    Given the global mapping:
      """json
      { "request": { "urlPath": "/missing" }, "response": { "status": 200, "bodyFileName": "nope.json" } }
      """
    When I send a "GET" request to "/missing"
    Then the response status is 500
    And the response body contains "nope.json"

  Scenario: HEAD requests receive headers without a body
    Given the global mapping:
      """json
      { "request": { "method": "ANY", "urlPath": "/head" }, "response": { "status": 200, "body": "content" } }
      """
    When I send a "HEAD" request to "/head"
    Then the response status is 200
    And the response body is empty
