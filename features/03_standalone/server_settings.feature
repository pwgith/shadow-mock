@standalone @P3 @FR-6.5 @NFR-5.3 @NFR-5.4
Feature: Server settings that change behaviour
  Settings an operator can change in configuration, each shown to change what
  a client sees.

  Scenario: admin.globalWrites deny reserves global changes but leaves sessions open
    Given Shadow Mock is running with configuration:
      """yaml
      admin: { globalWrites: deny }
      """
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    Then the response status is 403
    And the response JSON at "$.error" is "global_writes_via_management_app"
    When I call admin "GET" "/mappings"
    Then the response status is 200
    Given a key-based shadow mock "S1"
    When I call admin "POST" "/sessions/${S1.id}/mappings" with:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200, "body": "session" } }
      """
    Then the response status is 201
    And a "GET" request to "/x" with key "${S1.key}" returns body "session"

  Scenario: respond.gzip off stops compression even when the client accepts it
    Given Shadow Mock is running with configuration:
      """yaml
      respond: { gzip: "off" }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/big" }, "response": { "status": 200, "body": "${text:4096}" } }
      """
    When I send a request:
      | method | path | header:Accept-Encoding |
      | GET    | /big | gzip                   |
    Then the response header "Content-Encoding" is absent
    And the decoded response body has 4096 characters

  Scenario: sessions.detect.minTokenLength changes the shortest token accepted
    Given Shadow Mock is running with configuration:
      """yaml
      sessions: { detect: { minTokenLength: 10 } }
      """
    When I call admin "POST" "/sessions/key-based" with:
      """json
      { "tokens": ["ABC12345"] }
      """
    Then the response status is 422
    And the response JSON at "$.error" is "token_too_short"
    When I call admin "POST" "/sessions/key-based" with:
      """json
      { "tokens": ["ABC1234567"] }
      """
    Then the response status is 201

  Scenario: server.publicBaseUrl sets the base URL handed out for URL-based shadow mocks
    Given Shadow Mock is running with configuration:
      """yaml
      server: { publicBaseUrl: "https://mocks.example.com" }
      """
    When I call admin "POST" "/sessions/url-based" with:
      """json
      { "name": "orders-api-ci" }
      """
    Then the response status is 201
    And the response JSON at "$.baseUrl" matches "^https://mocks\.example\.com/d/[0-9a-z]+$"

  Scenario: limits.maxBodyBytes refuses larger request bodies
    Given Shadow Mock is running with configuration:
      """yaml
      limits: { maxBodyBytes: 100 }
      """
    And the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/in" }, "response": { "status": 200 } }
      """
    When I send a "POST" request to "/in" with body:
      """
      ${text:50}
      """
    Then the response status is 200
    When I send a "POST" request to "/in" with body:
      """
      ${text:200}
      """
    Then the response status is 413
    And the response JSON at "$.error" is "body_too_large"

  Scenario: egress.blockPrivate stops a proxy mapping reaching a private address
    Given Shadow Mock is running with configuration:
      """yaml
      egress: { blockPrivate: true }
      """
    And the upstream stub server "payments" returns 200 with body "real" for "GET /payments/1"
    And the global mapping:
      """json
      { "request": { "urlPathPattern": "/payments/.*" }, "response": { "proxyBaseUrl": "${upstream.payments}" } }
      """
    When I send a "GET" request to "/payments/1"
    Then the response status is 502
    And the response JSON at "$.error" is "egress_blocked"

  Scenario: egress.blockPrivate stops a webhook reaching a private address
    Given Shadow Mock is running with configuration:
      """yaml
      egress: { blockPrivate: true }
      """
    And the webhook receiver "hooks" is listening
    And the global mapping:
      """json
      { "request": { "urlPath": "/pay" }, "response": { "status": 202 },
        "serveEventListeners": [ { "name": "webhook", "parameters": { "method": "POST", "url": "${webhook.hooks}/callback" } } ] }
      """
    When I send a "GET" request to "/pay"
    Then the response status is 202
    And the webhook receiver "hooks" receives nothing within 800 ms

  Scenario: storage.journal.hot.maxBodyBytes cuts the bodies the journal keeps
    Given Shadow Mock is running with configuration:
      """yaml
      storage: { journal: { hot: { maxBodyBytes: 100 } } }
      """
    And the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/in" }, "response": { "status": 200, "body": "ok" } }
      """
    When I send a "POST" request to "/in" with body:
      """
      ${text:300}
      """
    Then the response status is 200
    When I call admin "GET" "/requests"
    Then the response JSON at "$.requests[0].request.body" matches "^x{100}$"

  Scenario: The journal keeps a large response whole by default
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPath": "/big" }, "response": { "status": 200, "body": "${text:200000}END" } }
      """
    When I send a "GET" request to "/big"
    And I call admin "GET" "/requests"
    Then the response JSON at "$.requests[0].response.body" matches "END$"

  Scenario: storage.journal.hot.maxBytes drops the oldest entries to stay within a memory budget
    Given Shadow Mock is running with configuration:
      """yaml
      storage: { journal: { hot: { maxBytes: 30000 } } }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/page" }, "response": { "status": 200, "body": "${text:10000}" } }
      """
    When I send 6 "GET" requests to "/page"
    And I call admin "GET" "/requests"
    Then the response JSON at "$.requests" has 2 items
