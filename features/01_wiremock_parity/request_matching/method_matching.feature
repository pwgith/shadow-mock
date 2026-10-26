@wiremock @P0 @FR-2.2
Feature: HTTP method matching

  Background:
    Given Shadow Mock is running

  Scenario Outline: A mapping only matches its method, unless the method is ANY
    Given the global mapping:
      """json
      { "request": { "method": "<mapped>", "urlPath": "/m" }, "response": { "status": 200 } }
      """
    When I send a "<sent>" request to "/m"
    Then the response status is <status>

    Examples:
      | mapped | sent    | status |
      | GET    | GET     | 200    |
      | GET    | POST    | 404    |
      | POST   | POST    | 200    |
      | PUT    | PUT     | 200    |
      | PATCH  | PATCH   | 200    |
      | DELETE | DELETE  | 200    |
      | HEAD   | HEAD    | 200    |
      | OPTIONS| OPTIONS | 200    |
      | ANY    | GET     | 200    |
      | ANY    | DELETE  | 200    |

  Scenario: A mapping without a method matches any method
    Given the global mapping:
      """json
      { "request": { "urlPath": "/no-method" }, "response": { "status": 200 } }
      """
    When I send a "PATCH" request to "/no-method"
    Then the response status is 200
