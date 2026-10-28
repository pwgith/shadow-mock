@standalone @central-server @P4 @NFR-6.3
Feature: Prometheus metrics
  The server exposes Prometheus metrics for requests, matches, detection and the session
  detector, labelled only by org, layer, stage, status and method.

  Scenario: Prometheus metrics are exposed
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    And a key-based shadow mock "S1"
    And I send a "GET" request to "/x" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    When I scrape "/__metrics"
    Then the metrics include:
      | metric                                          |
      | shadowmock_requests_total                       |
      | shadowmock_matches_total{layer="..."}           |
      | shadowmock_detection_hits_total{stage="Header"} |
      | shadowmock_automaton_patterns                   |
      | shadowmock_automaton_rebuild_seconds            |
      | shadowmock_active_sessions                      |
    And no metric has a label for session id, path or client IP

  Scenario: Requests are counted by method and status
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    And I send 2 "GET" requests to "/x"
    And I send a "GET" request to "/missing"
    And I send a "POST" request to "/missing"
    When I scrape "/__metrics"
    Then the metrics include:
      | metric                                                    |
      | shadowmock_requests_total{method="GET",status="200"}      |
      | shadowmock_requests_total{method="GET",status="404"}      |
      | shadowmock_requests_total{method="POST",status="404"}     |
    And the metric "shadowmock_requests_total" has a value of at least 2

  Scenario: Matches are counted by the layer that served them
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPath": "/g" }, "response": { "status": 200 } }
      """
    And a key-based shadow mock "S1"
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/s" }, "response": { "status": 200 } }
      """
    And I send a "GET" request to "/g"
    And I send a "GET" request to "/s" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    When I scrape "/__metrics"
    Then the metrics include:
      | metric                                    |
      | shadowmock_matches_total{layer="global"}    |
      | shadowmock_matches_total{layer="key-based"} |

  Scenario: Unmatched requests are not counted as matches
    Given Shadow Mock is running
    And I send a "GET" request to "/missing"
    When I scrape "/__metrics"
    Then the metrics include:
      | metric                                          |
      | shadowmock_requests_total{status="404"}         |
    And the metrics have no series for "shadowmock_matches_total"

  Scenario: Detection hits are counted by the stage that found the session
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    And a key-based shadow mock "S1"
    And I send a "GET" request to "/x" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    And I send a "GET" request to "/x?shadow_mock_key=${S1.key}"
    When I scrape "/__metrics"
    Then the metrics include:
      | metric                                          |
      | shadowmock_detection_hits_total{stage="Header"} |
      | shadowmock_detection_hits_total{stage="Query"}  |

  Scenario: The detector reports its size and how long a rebuild took
    Given Shadow Mock is running
    And a key-based shadow mock "S1" with:
      """json
      { "tokens": ["CUST-99812"] }
      """
    And I send a "GET" request to "/x" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    When I scrape "/__metrics"
    Then the metric "shadowmock_automaton_patterns" has a value of at least 2
    And the metric "shadowmock_automaton_rebuild_seconds_count" has a value of at least 1

  Scenario: The number of active sessions is reported
    Given Shadow Mock is running
    And 3 key-based shadow mocks
    When I scrape "/__metrics"
    Then the metric "shadowmock_active_sessions" has a value of at least 3
