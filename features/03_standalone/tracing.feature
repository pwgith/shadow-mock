@standalone @central-server @P4 @NFR-6.3
Feature: Tracing and structured logs
  The server continues a caller's W3C trace, exports a span for each request, and writes one JSON
  log line per mock request that carries the trace id, so a log line and a trace can be found
  from each other.

  Scenario: Structured JSON logs with trace correlation
    Given Shadow Mock is running with tracing enabled
    When I send a "GET" request to "/x" with a W3C traceparent header
    Then the log line for the request is JSON with fields "trace_id", "layer" and "status"
    And an OpenTelemetry span is exported for the request

  Scenario: The span continues the caller's trace
    Given Shadow Mock is running with tracing enabled
    When I send a "GET" request to "/x" with a W3C traceparent header
    Then the span is named "mock GET" and continues the caller's trace
    And the log line for the request has the caller's trace id

  Scenario: A request with no trace context starts a trace of its own
    Given Shadow Mock is running with tracing enabled
    When I send a "GET" request to "/x"
    Then the log line for the request has a trace id of 32 hexadecimal characters
    And the span has the attribute "http.response.status_code" equal to "404"

  Scenario: The span says which layer and mapping served the request
    Given Shadow Mock is running with tracing enabled
    And the global mapping:
      """json
      { "id": "aaaaaaaa-0000-4000-8000-000000000001", "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/x" with a W3C traceparent header
    Then the span has the attribute "shadowmock.layer" equal to "global"
    And the span has the attribute "shadowmock.stub_id" equal to "aaaaaaaa-0000-4000-8000-000000000001"
    And the span has the attribute "http.response.status_code" equal to "200"
    And the log line for the request has "layer" equal to "global"
    And the log line for the request has "status" equal to "200"

  Scenario: A request answered from a shadow mock names its session and detection stage
    Given Shadow Mock is running with tracing enabled
    And a key-based shadow mock "S1"
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/x" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the span has the attribute "shadowmock.layer" equal to "key-based"
    And the span has the attribute "shadowmock.detection_stage" equal to "Header"
    And the log line for the request has "stage" equal to "Header"

  Scenario: The query string is never traced or logged
    Given Shadow Mock is running with tracing enabled
    And a key-based shadow mock "S1"
    When I send a "GET" request to "/x?shadow_mock_key=${S1.key}&token=hunter2"
    Then no log line mentions "hunter2"
    And no log line mentions "${S1.key}"
    And no span attribute mentions "hunter2"
    And no span attribute mentions "${S1.key}"

  Scenario: A trace the caller did not sample is not recorded
    Given Shadow Mock is running with tracing enabled
    When I send a "GET" request to "/x" with a W3C traceparent header that is not sampled
    Then no span is exported for the request
    But the log line for the request has the caller's trace id

  Scenario: The sample ratio decides for requests that arrive with no trace
    Given Shadow Mock is running with tracing enabled and a sample ratio of 0
    When I send a "GET" request to "/x"
    Then no span is exported for the request

  Scenario: Admin calls are traced too
    Given Shadow Mock is running with tracing enabled
    When I call admin "GET" "/version" with header "Traceparent" set to "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
    Then the span is named "admin GET" and continues the caller's trace
    And the span has the attribute "shadowmock.kind" equal to "admin"

  Scenario: A proxied call continues the trace
    Given Shadow Mock is running with tracing enabled
    And the upstream stub server "payments" returns 200 with body "real" for "GET /payments/1"
    And the global mapping:
      """json
      { "request": { "urlPathPattern": "/payments/.*" }, "response": { "proxyBaseUrl": "${upstream.payments}" } }
      """
    When I send a "GET" request to "/payments/1" with a W3C traceparent header
    Then the upstream "payments" received a traceparent in the caller's trace

  Scenario: Requests are not logged unless asked for
    Given Shadow Mock is running
    When I send a "GET" request to "/x"
    Then no request is logged

  Scenario: Tracing is off unless configured
    Given Shadow Mock is running
    When I send a "GET" request to "/x" with a W3C traceparent header
    Then no request is logged
