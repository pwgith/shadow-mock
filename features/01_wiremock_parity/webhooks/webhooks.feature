@wiremock @P3 @FR-6.2
Feature: Webhooks after a request is served
  Mappings can fire outbound webhooks (serveEventListeners or the older
  postServeActions), with templating and an optional delay.

  Background:
    Given Shadow Mock is running
    And the webhook receiver "hooks" is listening

  Scenario: A webhook fires with a templated body
    Given the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/payments" },
        "response": { "status": 202 },
        "serveEventListeners": [ { "name": "webhook", "parameters": {
            "method": "POST", "url": "${webhook.hooks}/callback",
            "headers": { "Content-Type": "application/json" },
            "body": "{ \"paymentRef\": \"{{jsonPath originalRequest.body '$.ref'}}\", \"status\": \"SETTLED\" }" } } ] }
      """
    When I send a "POST" request to "/payments" with body:
      """json
      { "ref": "P-77" }
      """
    Then the response status is 202
    And within 2 s the webhook receiver "hooks" receives "POST /callback" with JSON at "$.paymentRef" equal to "P-77"

  Scenario: A delayed webhook fires after its delay
    Given the global mapping:
      """json
      { "request": { "urlPath": "/later" }, "response": { "status": 200 },
        "postServeActions": [ { "name": "webhook", "parameters": {
            "method": "GET", "url": "${webhook.hooks}/later", "delay": { "type": "fixed", "milliseconds": 1000 } } } ] }
      """
    When I send a "GET" request to "/later"
    Then the webhook receiver "hooks" receives nothing within 800 ms
    And within 2 s the webhook receiver "hooks" receives "GET /later"

  Scenario: A failing webhook does not affect the served response
    Given the webhook receiver "hooks" responds 500 to everything
    And the global mapping:
      """json
      { "request": { "urlPath": "/w" }, "response": { "status": 200 },
        "serveEventListeners": [ { "name": "webhook", "parameters": { "method": "POST", "url": "${webhook.hooks}/x" } } ] }
      """
    When I send a "GET" request to "/w"
    Then the response status is 200

  @saas @security
  Scenario: Webhooks to private addresses are blocked in SaaS
    Given a key-based shadow mock "S1" with the mapping:
      """json
      { "request": { "urlPath": "/w" }, "response": { "status": 200 },
        "serveEventListeners": [ { "name": "webhook", "parameters": { "method": "GET", "url": "http://169.254.169.254/latest" } } ] }
      """
    When I send a "GET" request to "/w" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 200
    And the engine log records "egress_blocked" for the webhook
