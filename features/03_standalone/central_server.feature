@central-server @standalone @P4 @NFR-5.2 @NFR-6.3
Feature: Central server deployment
  The standalone binary deployed as a long-running shared server for many
  teams, with a separate admin listener and observability.

  Scenario: Structured JSON logs with trace correlation
    Given Shadow Mock is running with tracing enabled
    When I send a "GET" request to "/x" with a W3C traceparent header
    Then the log line for the request is JSON with fields "trace_id", "layer" and "status"
    And an OpenTelemetry span is exported for the request
