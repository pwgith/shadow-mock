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

  @p3-proxy
  Scenario: Snapshot creates mappings from proxied traffic
    When I call admin "POST" "/recordings/snapshot" with:
      """json
      { "persist": false }
      """
    Then the response JSON at "$.mappings[0].request.url" is "/claims/2"
    And the response JSON at "$.mappings[0].response.jsonBody.id" is 2
