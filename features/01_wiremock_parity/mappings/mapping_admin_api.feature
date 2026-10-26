@wiremock @P0 @FR-6.3 @FR-6.4
Feature: Manage global mappings through the WireMock admin API
  As a test author using a WireMock client library
  I want the /__admin/mappings API to behave exactly like WireMock's
  So that my existing tooling keeps working

  In SaaS mode global writes go through the management app (see
  04_saas/auth/global_writes_restriction.feature), so these scenarios run
  in standalone mode, or in SaaS mode against a session scope.

  Background:
    Given Shadow Mock is running

  @standalone
  Scenario: Create a mapping and get it back with a generated id
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "method": "GET", "url": "/hello" }, "response": { "status": 200, "body": "hi" } }
      """
    Then the response status is 201
    And the response JSON at "$.id" is a UUID
    When I send a "GET" request to "/hello"
    Then the response body is "hi"

  @standalone
  Scenario: List mappings with paging
    Given 30 global mappings exist
    When I call admin "GET" "/mappings?limit=10&offset=10"
    Then the response JSON at "$.mappings" has 10 items
    And the response JSON at "$.meta.total" is 30

  @standalone
  Scenario: Get, replace and delete a mapping by id
    Given the global mapping:
      """json
      { "id": "aaaaaaaa-0000-0000-0000-000000000001", "request": { "url": "/a" }, "response": { "status": 200, "body": "v1" } }
      """
    When I call admin "PUT" "/mappings/aaaaaaaa-0000-0000-0000-000000000001" with:
      """json
      { "request": { "url": "/a" }, "response": { "status": 200, "body": "v2" } }
      """
    Then the response status is 200
    And a "GET" request to "/a" returns body "v2"
    When I call admin "DELETE" "/mappings/aaaaaaaa-0000-0000-0000-000000000001"
    Then the response status is 200
    And a "GET" request to "/a" returns status 404

  Scenario: Getting an unknown mapping returns 404
    When I call admin "GET" "/mappings/00000000-0000-0000-0000-000000000000"
    Then the response status is 404

  @standalone
  Scenario: Import several mappings at once
    When I call admin "POST" "/mappings/import" with:
      """json
      { "mappings": [
          { "request": { "url": "/i1" }, "response": { "status": 200 } },
          { "request": { "url": "/i2" }, "response": { "status": 200 } } ],
        "importOptions": { "duplicatePolicy": "IGNORE", "deleteAllNotInImport": false } }
      """
    Then the response status is 200
    And a "GET" request to "/i2" returns status 200

  @standalone
  Scenario: Delete all mappings
    Given 5 global mappings exist
    When I call admin "DELETE" "/mappings"
    Then admin "GET" "/mappings" returns 0 mappings

  @standalone
  Scenario: Reset mappings restores those loaded from files
    Given 2 global mappings were loaded from files
    And I call admin "POST" "/mappings" with:
      """json
      { "request": { "url": "/temp" }, "response": { "status": 200 } }
      """
    When I call admin "POST" "/mappings/reset"
    Then admin "GET" "/mappings" returns 2 mappings
    And a "GET" request to "/temp" returns status 404

  @standalone
  Scenario: Save persists API-created mappings to the mappings folder
    Given I call admin "POST" "/mappings" with:
      """json
      { "request": { "url": "/persist-me" }, "response": { "status": 200 }, "persistent": true }
      """
    When I call admin "POST" "/mappings/save"
    Then a file under "mappings/" contains "/persist-me"

  @standalone
  Scenario: Find and remove mappings by metadata
    Given the global mapping:
      """json
      { "request": { "url": "/m1" }, "response": { "status": 200 }, "metadata": { "team": "claims" } }
      """
    And the global mapping:
      """json
      { "request": { "url": "/m2" }, "response": { "status": 200 }, "metadata": { "team": "billing" } }
      """
    When I call admin "POST" "/mappings/find-by-metadata" with:
      """json
      { "matchesJsonPath": { "expression": "$.team", "equalTo": "claims" } }
      """
    Then the response JSON at "$.mappings" has 1 items
    When I call admin "POST" "/mappings/remove-by-metadata" with:
      """json
      { "matchesJsonPath": { "expression": "$.team", "equalTo": "claims" } }
      """
    Then a "GET" request to "/m1" returns status 404
    And a "GET" request to "/m2" returns status 200

  @standalone
  Scenario Outline: An invalid mapping is rejected with a helpful error
    When I call admin "POST" "/mappings" with:
      """json
      <mapping>
      """
    Then the response status is 422
    And the response JSON at "$.errors[0].detail" contains "<message>"

    Examples:
      | mapping                                                                              | message                    |
      | { "request": { "url": "/x", "urlPath": "/x" }, "response": { "status": 200 } }       | only one of url, urlPath   |
      | { "request": { "url": "/x" }, "response": { "status": 99 } }                         | status                     |
      | { "request": { "url": "/x", "bodyPatterns": [ { "noSuchMatcher": 1 } ] }, "response": { "status": 200 } } | unknown matcher |

  @standalone
  Scenario: The admin path prefix is configurable
    Given Shadow Mock is running with configuration:
      """yaml
      adminPrefix: /__control
      """
    When I send a "GET" request to "/__control/mappings"
    Then the response status is 200
