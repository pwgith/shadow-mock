@wiremock @standalone @P3 @FR-6.1
Feature: Snapshot recording from the journal
  Turn requests already proxied through Shadow Mock into mappings.

  Background:
    Given Shadow Mock is running
    And the upstream stub server "claims" returns 200 with JSON '{"id":2}' for "GET /claims/2"
    And the global mapping:
      """json
      { "request": { "urlPathPattern": "/claims/.*" }, "response": { "proxyBaseUrl": "${upstream.claims}" } }
      """
    And I send a "GET" request to "/claims/2"

  Scenario: Snapshot creates mappings from proxied traffic
    When I call admin "POST" "/recordings/snapshot" with:
      """json
      { "persist": false }
      """
    Then the response JSON at "$.mappings[0].request.url" is "/claims/2"
    And the response JSON at "$.mappings[0].response.jsonBody" is absent
    And the response JSON at "$.mappings[0].name" is "claims_2"

  Scenario: A request with a query string is recorded as a path and parameters
    Given the upstream stub server "claims" returns 200 with JSON '{"id":3}' for "GET /claims/3"
    And I send a "GET" request to "/claims/3?a=1&a=2&b=x%20y"
    When I call admin "POST" "/recordings/snapshot" with:
      """json
      { "persist": false, "filters": { "urlPath": "/claims/3" } }
      """
    Then the response JSON at "$.mappings[0].request.urlPath" is "/claims/3"
    And the response JSON at "$.mappings[0].request.queryParameters.a.hasExactly[1].equalTo" is "2"
    And the response JSON at "$.mappings[0].request.queryParameters.b.hasExactly[0].equalTo" is "x y"

  Scenario: The output format IDS lists only the ids
    When I call admin "POST" "/recordings/snapshot" with:
      """json
      { "persist": false, "outputFormat": "IDS" }
      """
    Then the response JSON at "$.ids[0]" is a UUID
    And the response JSON at "$.mappings" is absent

  Scenario: Big bodies can be kept in files
    When I call admin "POST" "/recordings/snapshot" with:
      """json
      { "extractBodyCriteria": { "textSizeThreshold": "0 kb", "binarySizeThreshold": "0 kb" } }
      """
    Then the response JSON at "$.mappings[0].response.body" is absent
    When I call admin "GET" "/files"
    Then the response body contains "claims_2-"

  Scenario: A size with a unit WireMock does not know is refused
    When I call admin "POST" "/recordings/snapshot" with:
      """json
      { "extractBodyCriteria": { "textSizeThreshold": "100 b" } }
      """
    Then the response status is 422
