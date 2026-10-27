@wiremock @P0 @FR-2.1
Feature: URL matching
  WireMock offers five ways to match the request URL. Each has precise
  semantics about the query string and about regex versus literal matching.

  Background:
    Given Shadow Mock is running

  Scenario Outline: Each URL matcher accepts or rejects the request
    Given the global mapping:
      """json
      { "request": { "method": "GET", "<matcher>": "<value>" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "<requested>"
    Then the response status is <status>

    Examples: url: exact path and query
      | matcher | value              | requested          | status |
      | url     | /claims?status=OPEN | /claims?status=OPEN | 200    |
      | url     | /claims?status=OPEN | /claims             | 404    |
      | url     | /claims             | /claims?status=OPEN | 404    |

    Examples: urlPath: exact path, any query
      | matcher | value   | requested           | status |
      | urlPath | /claims | /claims?status=OPEN | 200    |
      | urlPath | /claims | /claims/1           | 404    |

    Examples: urlPattern: regex over path and query
      | matcher    | value              | requested          | status |
      | urlPattern | /claims/[0-9]+     | /claims/42         | 200    |
      | urlPattern | /claims/[0-9]+     | /claims/abc        | 404    |
      | urlPattern | /claims\\\\?page=\\\\d | /claims?page=2     | 200    |

    Examples: urlPathPattern: regex over the path only
      | matcher        | value          | requested          | status |
      | urlPathPattern | /claims/[0-9]+ | /claims/42?x=1     | 200    |
      | urlPathPattern | /claims/[0-9]+ | /claims/42/notes   | 404    |

  Scenario: urlPathTemplate matches the path and its parameter matchers
    Given the global mapping:
      """json
      { "request": { "urlPathTemplate": "/claims/{claimId}/notes/{noteId}",
                     "pathParameters": { "claimId": { "matches": "[0-9]+" } } },
        "response": { "status": 200 } }
      """
    When I send a "GET" request to "/claims/42/notes/7"
    Then the response status is 200
    When I send a "GET" request to "/claims/abc/notes/7"
    Then the response status is 404
    When I send a "GET" request to "/claims/42"
    Then the response status is 404

  # Needs response templating ({{request.path.*}}).
  @P3
  Scenario: urlPathTemplate matches and exposes path parameters
    Given the global mapping:
      """json
      { "request": { "urlPathTemplate": "/claims/{claimId}/notes/{noteId}",
                     "pathParameters": { "claimId": { "matches": "[0-9]+" } } },
        "response": { "status": 200, "body": "{{request.path.claimId}}-{{request.path.noteId}}",
                      "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/claims/42/notes/7"
    Then the response status is 200
    And the response body is "42-7"
    When I send a "GET" request to "/claims/abc/notes/7"
    Then the response status is 404

  Scenario: URL matching is case-sensitive like WireMock
    Given the global mapping:
      """json
      { "request": { "urlPath": "/Claims" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/claims"
    Then the response status is 404

  Scenario: Percent-encoded paths are matched against the decoded form consistently with WireMock
    Given the global mapping:
      """json
      { "request": { "urlPath": "/files/my%20doc.pdf" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/files/my%20doc.pdf"
    Then the response status is 200
