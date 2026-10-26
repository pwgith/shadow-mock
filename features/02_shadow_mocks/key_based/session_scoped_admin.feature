@key-based @P1 @FR-7.3 @NFR-1.2
Feature: Session-scoped admin API
  Every WireMock admin endpoint for mappings, journal, scenarios and near
  misses is also available per session, in the same shape. Existing WireMock
  clients can be scoped by base URL or by a header.

  Background:
    Given Shadow Mock is running
    And a key-based shadow mock "S1"

  Scenario Outline: Session-scoped endpoints mirror the global ones
    When I call admin "<method>" "/sessions/${S1.id}<path>"<payload>
    Then the response status is <status>

    Examples:
      | method | path                         | payload                                                                                  | status |
      | POST   | /mappings                    |  with JSON '{"request":{"urlPath":"/x"},"response":{"status":200}}'                     | 201    |
      | GET    | /mappings                    |                                                                                          | 200    |
      | DELETE | /mappings                    |                                                                                          | 200    |
      | POST   | /mappings/import             |  with JSON '{"mappings":[{"request":{"urlPath":"/y"},"response":{"status":200}}]}'      | 200    |
      | GET    | /requests                    |                                                                                          | 200    |
      | POST   | /requests/count              |  with JSON '{"method":"ANY"}'                                                            | 200    |
      | POST   | /requests/find               |  with JSON '{"method":"ANY"}'                                                            | 200    |
      | GET    | /requests/unmatched          |                                                                                          | 200    |
      | DELETE | /requests                    |                                                                                          | 200    |
      | GET    | /scenarios                   |                                                                                          | 200    |
      | POST   | /scenarios/reset             |                                                                                          | 200    |
      | POST   | /near-misses/request         |  with JSON '{"method":"GET","url":"/x"}'                                                 | 200    |
      | GET    | /requests/unmatched/near-misses |                                                                                       | 200    |

  Scenario: The X-Shadow-Mock-Key header scopes a global admin endpoint to the session
    When I call admin "POST" "/mappings" with header "X-Shadow-Mock-Key" set to "${S1.key}" and body:
      """json
      { "request": { "urlPath": "/scoped" }, "response": { "status": 200, "body": "scoped" } }
      """
    Then the response status is 201
    And admin "GET" "/sessions/${S1.id}/mappings" lists 1 mappings
    And a "GET" request to "/scoped" returns status 404
    And a "GET" request to "/scoped" with key "${S1.key}" returns body "scoped"

  Scenario: The header can carry the session id instead of the key
    When I call admin "GET" "/mappings" with header "X-Shadow-Mock-Key" set to "${S1.id}"
    Then the response is the mapping list of "S1"

  Scenario: A WireMock client pointed at the session base URL manages only the session
    Given a WireMock Java client configured with base URL "{admin base}/sessions/${S1.id}"
    When the client calls stubFor(get("/client").willReturn(ok("from-client")))
    Then admin "GET" "/sessions/${S1.id}/mappings" lists 1 mappings
    And admin "GET" "/mappings" does not include a mapping for "/client"

  Scenario: A session admin call for an unknown session returns 404
    When I call admin "GET" "/sessions/ses_00000000000000000000000000/mappings"
    Then the response status is 404
