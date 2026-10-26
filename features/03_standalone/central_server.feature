@central-server @standalone @P4 @NFR-5.2 @NFR-6.3
Feature: Central server deployment
  The standalone binary deployed as a long-running shared server for many
  teams, with a separate admin listener and observability.

  Scenario: Admin API on a separate listener
    Given Shadow Mock is running with configuration:
      """yaml
      listen: { mock: ":8080", admin: ":9090" }
      """
    When I send a "GET" request to "http://localhost:8080/__admin/mappings"
    Then the response status is 404
    When I send a "GET" request to "http://localhost:9090/__admin/mappings"
    Then the response status is 200

  Scenario: Restrict mock traffic on a shared network with a local IP allowlist
    Given Shadow Mock is running with configuration:
      """yaml
      tenancy: { local: { ipAllowlist: ["10.20.0.0/16"] } }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/x" from IP "10.20.3.4"
    Then the response status is 200
    When I send a "GET" request to "/x" from IP "192.168.1.9"
    Then the response status is 403

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
