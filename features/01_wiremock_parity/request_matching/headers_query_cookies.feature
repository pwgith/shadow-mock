@wiremock @P0 @FR-2.3
Feature: Matching on headers, query parameters, cookies and form parameters

  Background:
    Given Shadow Mock is running

  Scenario: Header matching is case-insensitive on the header name
    Given the global mapping:
      """json
      { "request": { "urlPath": "/h", "headers": { "X-Tenant": { "equalTo": "acme" } } },
        "response": { "status": 200 } }
      """
    When I send a request:
      | method | path | header:x-tenant |
      | GET    | /h   | acme            |
    Then the response status is 200

  Scenario Outline: Query parameter matching
    Given the global mapping:
      """json
      { "request": { "urlPath": "/q", "queryParameters": { "status": { "equalTo": "OPEN" }, "page": { "matches": "[0-9]+" } } },
        "response": { "status": 200 } }
      """
    When I send a "GET" request to "<url>"
    Then the response status is <status>

    Examples:
      | url                      | status |
      | /q?status=OPEN&page=2    | 200    |
      | /q?page=2&status=OPEN    | 200    |
      | /q?status=CLOSED&page=2  | 404    |
      | /q?status=OPEN           | 404    |
      | /q?status=OPEN&page=two  | 404    |

  Scenario Outline: Multi-value query parameters with hasExactly and including
    Given the global mapping:
      """json
      { "request": { "urlPath": "/multi", "queryParameters": { "id": { "<operator>": [ { "equalTo": "1" }, { "equalTo": "2" } ] } } },
        "response": { "status": 200 } }
      """
    When I send a "GET" request to "<url>"
    Then the response status is <status>

    Examples:
      | operator   | url                  | status |
      | hasExactly | /multi?id=1&id=2     | 200    |
      | hasExactly | /multi?id=1&id=2&id=3| 404    |
      | including  | /multi?id=1&id=2&id=3| 200    |
      | including  | /multi?id=1          | 404    |

  Scenario: Cookie matching
    Given the global mapping:
      """json
      { "request": { "urlPath": "/c", "cookies": { "session": { "matches": "^s-[a-z]+$" } } },
        "response": { "status": 200 } }
      """
    When I send a request:
      | method | path | cookie:session |
      | GET    | /c   | s-abc          |
    Then the response status is 200
    When I send a request:
      | method | path | cookie:session |
      | GET    | /c   | x-abc          |
    Then the response status is 404

  Scenario: Form parameter matching for application/x-www-form-urlencoded
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/form", "formParameters": { "grant_type": { "equalTo": "client_credentials" } } },
        "response": { "status": 200 } }
      """
    When I send a request:
      | method | path  | header:Content-Type               | body                                |
      | POST   | /form | application/x-www-form-urlencoded | grant_type=client_credentials&x=1   |
    Then the response status is 200

  Scenario: absent requires that a header is not sent
    Given the global mapping:
      """json
      { "request": { "urlPath": "/anon", "headers": { "Authorization": { "absent": true } } },
        "response": { "status": 200 } }
      """
    When I send a "GET" request to "/anon"
    Then the response status is 200
    When I send a request:
      | method | path  | header:Authorization |
      | GET    | /anon | Bearer x             |
    Then the response status is 404
