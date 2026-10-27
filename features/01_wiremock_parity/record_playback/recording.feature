@wiremock @standalone @P3 @FR-6.1
Feature: Record and playback into the global session
  Record real traffic through Shadow Mock and turn it into mappings.
  Recording into the global session is a global write, so in SaaS mode
  recording happens into a session instead
  (see 02_shadow_mocks/common/recording_into_sessions.feature).

  Background:
    Given Shadow Mock is running
    And the upstream stub server "claims" returns 200 with JSON '{"id":1}' for "GET /claims/1"

  @p3-proxy
  Scenario: Start, check status and stop a recording
    When I call admin "POST" "/recordings/start" with:
      """json
      { "targetBaseUrl": "${upstream.claims}" }
      """
    Then admin "GET" "/recordings/status" returns status "Recording"
    When I send a "GET" request to "/claims/1"
    Then the response JSON at "$.id" is 1
    When I call admin "POST" "/recordings/stop"
    Then the response JSON at "$.mappings" has 1 items
    And the response JSON at "$.mappings[0].request.url" is "/claims/1"
    And admin "GET" "/recordings/status" returns status "Stopped"

  @p3-proxy
  Scenario: Recorded mappings play back without the upstream
    Given I record "GET /claims/1" from "${upstream.claims}"
    And the upstream stub server "claims" is stopped
    When I send a "GET" request to "/claims/1"
    Then the response JSON at "$.id" is 1

  @p3-proxy
  Scenario: Recording with a filter and request-body capture
    When I call admin "POST" "/recordings/start" with:
      """json
      { "targetBaseUrl": "${upstream.claims}",
        "filters": { "urlPathPattern": "/claims/.*" },
        "captureHeaders": { "Accept": {} },
        "requestBodyPattern": { "matcher": "equalToJson", "ignoreArrayOrder": true } }
      """
    And I send a "GET" request to "/claims/1"
    And I send a "GET" request to "/other"
    When I call admin "POST" "/recordings/stop"
    Then the response JSON at "$.mappings" has 1 items

  @p3-proxy
  Scenario: Repeated identical requests become call-count responses when requested
    Given the upstream stub server "claims" returns 202 then 200 for "GET /claims/1"
    When I call admin "POST" "/recordings/start" with:
      """json
      { "targetBaseUrl": "${upstream.claims}", "repeatsAsCallResponses": true }
      """
    And I send 2 "GET" requests to "/claims/1"
    When I call admin "POST" "/recordings/stop"
    Then the response JSON at "$.mappings" has 1 items
    And the response JSON at "$.mappings[0].callResponses" is present
