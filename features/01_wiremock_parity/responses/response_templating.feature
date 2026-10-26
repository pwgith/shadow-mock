@wiremock @P3 @FR-3.4
Feature: Response templating
  Handlebars templates with the WireMock helper set, enabled per mapping
  with the response-template transformer or globally by configuration.

  Background:
    Given Shadow Mock is running
    And the test clock is at "2026-11-01T09:30:00Z"

  Scenario Outline: Request model helpers
    Given the global mapping:
      """json
      { "request": { "urlPathPattern": "/t/.*" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a request:
      | method | path                  | header:X-Corr | cookie:sid | body          |
      | POST   | /t/claims/42?status=O | c-9           | s-1        | {"amount":15} |
    Then the response body is "<result>"

    Examples:
      | template                                | result           |
      | {{request.method}}                      | POST             |
      | {{request.path}}                        | /t/claims/42     |
      | {{request.pathSegments.[2]}}            | 42               |
      | {{request.query.status}}                | O                |
      | {{request.headers.X-Corr}}              | c-9              |
      | {{request.cookies.sid}}                 | s-1              |
      | {{jsonPath request.body '$.amount'}}    | 15               |
      | {{request.url}}                         | /t/claims/42?status=O |

  Scenario Outline: Utility helpers
    Given the global mapping:
      """json
      { "request": { "urlPath": "/u" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/u"
    Then the response body matches "<pattern>"

    Examples:
      | template                                                 | pattern                       |
      | {{now format='yyyy-MM-dd'}}                              | ^2026-11-01$                  |
      | {{now offset='2 days' format='yyyy-MM-dd'}}              | ^2026-11-03$                  |
      | {{randomValue length=8 type='ALPHANUMERIC'}}             | ^[A-Za-z0-9]{8}$              |
      | {{randomValue type='UUID'}}                              | ^[0-9a-f-]{36}$               |
      | {{math 3 '*' 4}}                                         | ^12$                          |
      | {{base64 'hello'}}                                       | ^aGVsbG8=$                    |
      | {{regexExtract 'ab-123' '[0-9]+'}}                       | ^123$                         |
      | {{#if true}}yes{{else}}no{{/if}}                         | ^yes$                         |
      | {{#each (array 1 2 3)}}{{this}}{{/each}}                 | ^123$                         |

  Scenario: XPath helper over an XML request body
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/xml" },
        "response": { "status": 200, "body": "{{xPath request.body '/claim/id/text()'}}", "transformers": ["response-template"] } }
      """
    When I send a "POST" request to "/xml" with body:
      """xml
      <claim><id>77</id></claim>
      """
    Then the response body is "77"

  Scenario: parseJson and a templated jsonBody
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/echo" },
        "response": { "status": 200, "jsonBody": { "received": "{{jsonPath request.body '$.name'}}" }, "transformers": ["response-template"] } }
      """
    When I send a "POST" request to "/echo" with body:
      """json
      { "name": "Ada" }
      """
    Then the response JSON at "$.received" is "Ada"

  Scenario: Templated headers and a templated bodyFileName
    Given the file "__files/claim-42.json":
      """json
      { "id": 42 }
      """
    And the global mapping:
      """json
      { "request": { "urlPathTemplate": "/claims/{id}" },
        "response": { "status": 200, "bodyFileName": "claim-{{request.path.id}}.json",
                      "headers": { "X-Claim": "{{request.path.id}}" }, "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/claims/42"
    Then the response header "X-Claim" is "42"
    And the response JSON at "$.id" is 42

  Scenario: Templates are not rendered unless enabled
    Given the global mapping:
      """json
      { "request": { "urlPath": "/raw" }, "response": { "status": 200, "body": "{{request.path}}" } }
      """
    When I send a "GET" request to "/raw"
    Then the response body is "{{request.path}}"

  @standalone
  Scenario: Global templating renders every response
    Given Shadow Mock is running with configuration:
      """yaml
      templating: { global: true }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/g" }, "response": { "status": 200, "body": "{{request.path}}" } }
      """
    When I send a "GET" request to "/g"
    Then the response body is "/g"

  @security @NFR-5.3
  Scenario: Templates cannot read files outside __files
    Given the global mapping:
      """json
      { "request": { "urlPath": "/evil" },
        "response": { "status": 200, "bodyFileName": "../../etc/passwd", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/evil"
    Then the response status is 500
    And the response body does not contain "root:"
