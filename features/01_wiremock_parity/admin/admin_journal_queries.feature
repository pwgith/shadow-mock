@wiremock @P3 @standalone @FR-10.7
Feature: Journal queries and the shape of journal answers
  GET /__admin/requests narrows the journal the way WireMock does, and every journal answer says
  whether the journal is switched off. Every expectation below was checked against a real
  WireMock 3.13.2.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "id": "aaaaaaaa-0000-4000-8000-000000000001", "request": { "urlPath": "/a" },
        "response": { "status": 200, "body": "A" }, "metadata": { "team": "claims" } }
      """
    And the global mapping:
      """json
      { "id": "bbbbbbbb-0000-4000-8000-000000000002", "request": { "urlPath": "/b" }, "response": { "status": 201 } }
      """
    And I send a "GET" request to "/a"
    And I send a "GET" request to "/b"
    And I send a "GET" request to "/nomatch1"
    And I send a "GET" request to "/a"
    And I send a "GET" request to "/nomatch2"

  Scenario Outline: The query narrows the list, and meta.total counts what the filters leave
    When I call admin "GET" "/requests<query>"
    Then the response JSON at "$.requests" has <shown> items
    And the response JSON at "$.meta.total" is <total>

    Examples:
      | query                                                              | shown | total |
      |                                                                    | 5     | 5     |
      | ?limit=0                                                           | 0     | 5     |
      | ?limit=2                                                           | 2     | 5     |
      | ?limit=50                                                          | 5     | 5     |
      | ?unmatched=true                                                    | 2     | 2     |
      | ?unmatched=false                                                   | 5     | 5     |
      | ?unmatched=maybe                                                   | 5     | 5     |
      | ?matchingStub=aaaaaaaa-0000-4000-8000-000000000001                 | 2     | 2     |
      | ?matchingStub=bbbbbbbb-0000-4000-8000-000000000002                 | 1     | 1     |
      | ?matchingStub=cccccccc-0000-4000-8000-000000000003                 | 0     | 0     |
      | ?matchingStub=aaaaaaaa-0000-4000-8000-000000000001&unmatched=true  | 0     | 0     |
      | ?unmatched=true&limit=1                                            | 1     | 2     |
      | ?since=2999-01-01T00:00:00Z                                        | 0     | 5     |

  Scenario Outline: A query value WireMock cannot read is refused with 400
    When I call admin "GET" "/requests<query>"
    Then the response status is 400

    Examples:
      | query              |
      | ?limit=-1          |
      | ?limit=many        |
      | ?matchingStub=nope |
      | ?since=yesterday   |

  Scenario Outline: Every journal answer says whether the journal is off
    When I call admin "POST" "<path>" with:
      """json
      { "urlPath": "/a" }
      """
    Then the response status is 200
    And the response JSON at "$.requestJournalDisabled" is false

    Examples:
      | path            |
      | /requests/find  |
      | /requests/count |

  Scenario: The list of unmatched requests says whether the journal is off
    When I call admin "GET" "/requests/unmatched"
    Then the response JSON at "$.requests" has 2 items
    And the response JSON at "$.requestJournalDisabled" is false

  Scenario: Removing requests answers with the serve events it removed
    When I call admin "POST" "/requests/remove" with:
      """json
      { "urlPath": "/a" }
      """
    Then the response JSON at "$.serveEvents" has 2 items
    And the response JSON at "$.serveEvents[0].request.url" is "/a"
    And the response JSON at "$.serveEvents[0].wasMatched" is true
    And admin "GET" "/requests" lists 3 requests

  Scenario: Removing requests by the metadata of the stub that served them
    When I call admin "POST" "/requests/remove-by-metadata" with:
      """json
      { "matchesJsonPath": "$.team" }
      """
    Then the response JSON at "$.serveEvents" has 2 items
    And admin "GET" "/requests" lists 3 requests
    When I call admin "POST" "/requests/remove-by-metadata" with:
      """json
      { "equalTo": "nothing like this" }
      """
    Then the response JSON at "$.serveEvents" has 0 items

  Scenario: A request that no stub served carries the report as a sub event
    When I call admin "GET" "/requests?unmatched=true&limit=1"
    Then the response JSON at "$.requests[0].subEvents[0].type" is "REQUEST_NOT_MATCHED"
    And the response JSON at "$.requests[0].subEvents[0].data.status" is 404
    And the response JSON at "$.requests[0].subEvents[0].data.report" contains "Request was not matched"

  Scenario: A request that a stub served has no sub events
    When I call admin "GET" "/requests?matchingStub=bbbbbbbb-0000-4000-8000-000000000002"
    Then the response JSON at "$.requests[0].subEvents" has 0 items

  Scenario: Deleting a request that is not in the journal is still a 200
    When I call admin "DELETE" "/requests/dddddddd-0000-4000-8000-000000000004"
    Then the response status is 200
    When I call admin "DELETE" "/requests/not-a-uuid"
    Then the response status is 400
