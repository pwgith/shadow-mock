@wiremock @P0 @FR-5.1 @FR-5.4
Feature: Request journal
  Every served request is recorded with the matched mapping, the response
  and timings, plus the tenant, session and layer that served it.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "id": "bbbbbbbb-0000-0000-0000-000000000001", "request": { "urlPath": "/j" }, "response": { "status": 200, "body": "ok" } }
      """

  Scenario: A served request appears in the journal with its details
    When I send a request:
      | method | path   | header:X-Test | body  |
      | POST   | /j?a=1 | t1            | hello |
    And I call admin "GET" "/requests"
    Then the response JSON at "$.requests[0].request.url" is "/j?a=1"
    And the response JSON at "$.requests[0].request.headers.X-Test" is "t1"
    And the response JSON at "$.requests[0].request.body" is "hello"
    And the response JSON at "$.requests[0].stubMapping.id" is "bbbbbbbb-0000-0000-0000-000000000001"
    And the response JSON at "$.requests[0].response.status" is 200
    And the response JSON at "$.requests[0].timing.totalTime" is a number

  Scenario: Journal entries record tenant, session and layer
    When I send a "GET" request to "/j"
    And I call admin "GET" "/requests"
    Then the response JSON at "$.requests[0].shadowMock.layer" is "global"
    And the response JSON at "$.requests[0].shadowMock.tenant" is the current org
    And the response JSON at "$.requests[0].shadowMock.sessionId" is null

  Scenario: Get one entry by id
    Given I send a "GET" request to "/j"
    And I call admin "GET" "/requests"
    And I remember the response JSON at "$.requests[0].id" as "RID"
    When I call admin "GET" "/requests/${RID}"
    Then the response JSON at "$.request.url" is "/j"

  Scenario: Clear the journal
    Given I send a "GET" request to "/j"
    When I call admin "DELETE" "/requests"
    Then admin "GET" "/requests" lists 0 requests

  @standalone
  Scenario Outline: Journal size limit and disabling
    Given Shadow Mock is running with configuration:
      """yaml
      storage: { journal: { hot: { maxEntries: <max> } } }
      """
    When I send 5 "GET" requests to "/j"
    Then admin "GET" "/requests" lists <listed> requests

    Examples:
      | max | listed |
      | 3   | 3      |
      | 0   | 0      |
