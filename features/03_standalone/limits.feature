@standalone @central-server @P4 @NFR-5.2
Feature: Quotas on a shared server
  A shared server can cap what one team uses so the rest are not starved. Every
  limit is off unless set, so a standalone server behaves like WireMock.

  Scenario: Shadow mocks are capped
    Given Shadow Mock is running with configuration:
      """yaml
      limits: { maxSessions: 1 }
      """
    When I call admin "POST" "/sessions/key-based" with:
      """json
      { "tokens": ["ABC12345"] }
      """
    Then the response status is 201
    When I call admin "POST" "/sessions/url-based" with:
      """json
      {}
      """
    Then the response status is 429
    And the response JSON at "$.error" is "session_limit_reached"

  Scenario: Mappings are capped per shadow mock
    Given Shadow Mock is running with configuration:
      """yaml
      limits: { maxMappingsPerSession: 1 }
      """
    And a key-based shadow mock "S1"
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/a" }, "response": { "status": 200 } }
      """
    When I call admin "POST" "/sessions/${S1.id}/mappings" with:
      """json
      { "request": { "urlPath": "/b" }, "response": { "status": 200 } }
      """
    Then the response status is 429
    And the response JSON at "$.error" is "mapping_limit_reached"

  Scenario: Global mappings are capped
    Given Shadow Mock is running with configuration:
      """yaml
      limits: { maxMappings: 1 }
      """
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/a" }, "response": { "status": 200 } }
      """
    Then the response status is 201
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/b" }, "response": { "status": 200 } }
      """
    Then the response status is 429
    And the response JSON at "$.error" is "mapping_limit_reached"

  Scenario: A delay longer than the limit is refused when the mapping is created
    Given Shadow Mock is running with configuration:
      """yaml
      limits: { maxDelay: 2s }
      """
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/slow" }, "response": { "status": 200, "fixedDelayMilliseconds": 60000 } }
      """
    Then the response status is 422
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/ok" }, "response": { "status": 200, "fixedDelayMilliseconds": 2000 } }
      """
    Then the response status is 201

  Scenario: A client over its request rate is told to wait
    Given Shadow Mock is running with configuration:
      """yaml
      limits: { requestsPerSecond: 1, burst: 2 }
      """
    And the test clock is at "2026-10-27T12:00:00Z"
    And the global mapping:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/x" from IP "10.0.0.1"
    Then the response status is 200
    When I send a "GET" request to "/x" from IP "10.0.0.1"
    Then the response status is 200
    When I send a "GET" request to "/x" from IP "10.0.0.1"
    Then the response status is 429
    And the response header "Retry-After" is "1"
    And the response JSON at "$.error" is "rate_limited"
    When I send a "GET" request to "/x" from IP "10.0.0.2"
    Then the response status is 200
    When the test clock advances by "1s"
    And I send a "GET" request to "/x" from IP "10.0.0.1"
    Then the response status is 200

  Scenario: Health checks and the admin API are not rate limited
    Given Shadow Mock is running with configuration:
      """yaml
      limits: { requestsPerSecond: 1, burst: 1 }
      """
    And the test clock is at "2026-10-27T12:00:00Z"
    When I send a "GET" request to "/__health" from IP "10.0.0.1"
    And I send a "GET" request to "/__health" from IP "10.0.0.1"
    Then the response status is 200
    When I call admin "GET" "/mappings"
    And I call admin "GET" "/mappings"
    Then the response status is 200

  Scenario: With no limits set nothing is refused
    Given Shadow Mock is running with configuration:
      """yaml
      log: { format: json }
      """
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/slow" }, "response": { "status": 200, "fixedDelayMilliseconds": 60000 } }
      """
    Then the response status is 201
