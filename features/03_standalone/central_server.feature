@central-server @standalone @P4 @NFR-5.2 @NFR-6.3
Feature: Central server deployment
  The standalone binary deployed as a long-running shared server for many
  teams, with a separate admin listener and observability.

  Scenario: Prometheus metrics are exposed
    Given Shadow Mock is running
    And a key-based shadow mock "S1"
    And I send a "GET" request to "/x" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    When I scrape "/__metrics"
    Then the metrics include:
      | metric                                   |
      | shadowmock_requests_total                |
      | shadowmock_matches_total{layer="..."}    |
      | shadowmock_detection_hits_total{stage="Header"} |
      | shadowmock_automaton_patterns            |
      | shadowmock_automaton_rebuild_seconds     |
    And no metric has a label for session id, path or client IP

  Scenario: Structured JSON logs with trace correlation
    Given Shadow Mock is running with tracing enabled
    When I send a "GET" request to "/x" with a W3C traceparent header
    Then the log line for the request is JSON with fields "trace_id", "layer" and "status"
    And an OpenTelemetry span is exported for the request
