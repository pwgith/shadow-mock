@wiremock @P3 @FR-10.6
Feature: The request model in templates
  Templates read the request through the same names WireMock gives them: request.id, the segments
  of request.path, request.requestLine, lookup keys such as ids[].1, and the parts of a multipart
  body. Every expectation below was checked against a real WireMock 3.13.2.

  Background:
    Given Shadow Mock is running

  Scenario Outline: Reading the path, the query and the request line
    Given the global mapping:
      """json
      { "request": { "urlPathPattern": "/r/.*" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/r/a/b?x=1&x=2&x=3&ids[]=p&ids[]=q"
    Then the response body equals '<body>'

    Examples:
      | template                                    | body  |
      | {{request.path.0}}                           | r     |
      | {{request.path.[1]}}                         | a     |
      | {{request.path.2}}                           | b     |
      | [{{request.path.[9]}}]                       | []    |
      | {{request.requestLine.method}}               | GET   |
      | {{request.requestLine.path}}                 | /r/a/b |
      | {{request.requestLine.pathSegments.[1]}}     | a     |
      | {{request.query.x}}                          | 1     |
      | {{request.query.x.[last]}}                   | 3     |
      | {{request.query.x.[1]}}                      | 2     |
      | {{size request.query.x}}                     | 3     |
      | {{lookup request.query 'ids[].0'}}           | p     |
      | {{lookup request.query 'ids[].1'}}           | q     |
      | {{lookup request.query.x 2}}                 | 3     |

  Scenario: request.id is the id of the request in the journal
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "{{request.id}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/h"
    Then the response body matches "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"

  Scenario Outline: A multipart body is read by the name of each part
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "<template>", "transformers": ["response-template"] } }
      """
    When I send a "POST" request to "/h" with multipart parts:
      | name | type       | body  |
      | f    | text/plain | hello |
      | g    | image/png  | PNG   |
    Then the response body equals '<body>'

    Examples:
      | template                                         | body             |
      | {{request.multipart}}                             | true             |
      | {{request.parts.f.body}}                          | hello            |
      | {{request.parts.f.bodyAsBase64}}                  | aGVsbG8=         |
      | {{request.parts.f.name}}                          | f                |
      | {{request.parts.f.headers.Content-Type}}          | text/plain       |
      | {{request.parts.f.binary}}                        | false            |
      | {{request.parts.g.binary}}                        | true             |
      | [{{request.parts.zz.body}}]                       | []               |

  Scenario: request.multipart is false for a request that is not multipart
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h" },
        "response": { "status": 200, "body": "{{request.multipart}}/[{{request.parts.f.body}}]", "transformers": ["response-template"] } }
      """
    When I send a "POST" request to "/h" with body:
      """
      {}
      """
    Then the response body equals 'false/[]'
