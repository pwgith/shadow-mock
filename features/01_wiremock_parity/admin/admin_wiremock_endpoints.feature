@wiremock @P3 @standalone @FR-10.8
Feature: The rest of WireMock's admin endpoints
  Settings through PUT, the proxy pass-through setting, deleting unmatched stubs, the API
  documents, the alias of the mappings list, the method WireMock fills in, and the names of
  persisted mapping files. Every expectation below was checked against a real WireMock 3.13.2.

  Background:
    Given Shadow Mock is running

  Scenario: Unmatched stubs are deleted, and stubs that served a request stay
    Given the global mapping:
      """json
      { "id": "aaaaaaaa-0000-4000-8000-000000000001", "request": { "urlPath": "/used" }, "response": { "status": 200 } }
      """
    And the global mapping:
      """json
      { "id": "bbbbbbbb-0000-4000-8000-000000000002", "request": { "urlPath": "/idle" }, "response": { "status": 200 } }
      """
    And I send a "GET" request to "/used"
    When I call admin "DELETE" "/mappings/unmatched"
    Then the response status is 200
    And admin "GET" "/mappings" returns 1 mapping
    And a "GET" request to "/used" returns status 200
    And a "GET" request to "/idle" returns status 404

  Scenario: Settings are replaced by PUT and by POST, and proxyPassThrough keeps its value when left out
    Then admin "GET" "/settings" returns JSON at "$.settings.proxyPassThrough" equal to "true"
    When I call admin "PUT" "/settings" with:
      """json
      { "proxyPassThrough": false, "extended": { "team": "claims" } }
      """
    Then the response status is 200
    And admin "GET" "/settings" returns JSON at "$.settings.proxyPassThrough" equal to "false"
    And admin "GET" "/settings" returns JSON at "$.settings.extended.team" equal to "claims"
    When I call admin "POST" "/settings" with:
      """json
      { "fixedDelay": 5 }
      """
    Then admin "GET" "/settings" returns JSON at "$.settings.proxyPassThrough" equal to "false"
    And admin "GET" "/settings" returns JSON at "$.settings.fixedDelay" equal to "5"

  Scenario: The stored mapping says ANY when the request names no method
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/any" }, "response": { "status": 200 } }
      """
    Then the response status is 201
    And the response JSON at "$.request.method" is "ANY"
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "method": "GET", "urlPath": "/get" }, "response": { "status": 200 } }
      """
    Then the response JSON at "$.request.method" is "GET"

  Scenario Outline: The API documents are served from the binary
    When I call admin "GET" "<path>"
    Then the response status is 200
    And the response header "Content-Type" contains "<type>"
    And the response body contains "<text>"

    Examples:
      | path          | type             | text            |
      | /docs         | text/html        | swagger-ui      |
      | /docs/swagger | application/json | openapi         |
      | /swagger-ui/  | text/html        | SwaggerUIBundle |

  Scenario: GET /__admin/ lists the mappings, as GET /__admin/mappings does
    Given the global mapping:
      """json
      { "request": { "urlPath": "/listed" }, "response": { "status": 200 } }
      """
    When I call admin "GET" "/"
    Then the response status is 200
    And the response JSON at "$.mappings" has 1 items
    And the response JSON at "$.meta.total" is 1

  Scenario Outline: A persisted mapping file is named after the mapping, then its id
    Given I call admin "POST" "/mappings" with:
      """json
      <mapping>
      """
    Then the folder "mappings" has a file named like "<file>-[0-9a-f-]{36}\.json"

    Examples:
      | mapping                                                                                             | file            |
      | { "name": "Get /users?id=1 (v2)", "request": { "url": "/x" }, "response": {}, "persistent": true } | get-usersid1-v2 |
      | { "request": { "urlPath": "/users/42" }, "response": {}, "persistent": true }                      | any-users42     |
      | { "request": { "method": "POST", "url": "/p" }, "response": {}, "persistent": true }               | post-p          |
