@wiremock @P3 @FR-3.3
Feature: Fault injection
  In standalone mode, faults are produced at the TCP level exactly as in
  WireMock. Behind the SaaS host's HTTP proxy, they reach the client as a
  proxy error.

  Background:
    Given Shadow Mock is running

  @standalone
  @p3-harness
  Scenario Outline: Each fault type in standalone mode
    Given the global mapping:
      """json
      { "request": { "urlPath": "/fault" }, "response": { "fault": "<fault>" } }
      """
    When I send a "GET" request to "/fault"
    Then <outcome>

    Examples:
      | fault                    | outcome                                          |
      | CONNECTION_RESET_BY_PEER | the connection is reset                          |
      | EMPTY_RESPONSE           | the connection closes with no response           |
      | MALFORMED_RESPONSE_CHUNK | the response fails with a malformed chunk error  |
      | RANDOM_DATA_THEN_CLOSE   | the client receives garbage then the connection closes |

  @saas
  Scenario: Faults behind the SaaS proxy surface as a 502
    Given the global mapping:
      """json
      { "request": { "urlPath": "/fault" }, "response": { "fault": "CONNECTION_RESET_BY_PEER" } }
      """
    When I send a "GET" request to "/fault"
    Then the response status is 502
