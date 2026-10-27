@key-based @P1 @FR-7.6 @FR-4.2
Feature: Call-count isolation between sessions
  Every session has its own call counters, starting at zero when it is
  created. One test's calls never change the call numbers another test or
  the global session sees.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "method": "POST", "urlPath": "/payments" },
        "response": { "status": 200 },
        "callResponses": [ { "calls": "1", "response": { "status": 503 } } ] }
      """

  Scenario: Each session sees call 1 for itself
    Given a key-based shadow mock "S1"
    And a key-based shadow mock "S2"
    When I send a "POST" request to "/payments" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 503
    When I send a "POST" request to "/payments" with header "X-Shadow-Mock-Key" set to "${S2.key}"
    Then the response status is 503
    When I send a "POST" request to "/payments" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 200

  Scenario: Session calls do not advance the global counter
    Given a key-based shadow mock "S1"
    And I send 3 "POST" requests to "/payments" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    When I send a "POST" request to "/payments"
    Then the response status is 503

  Scenario: A new session starts at zero even after heavy global traffic
    Given I send 50 "POST" requests to "/payments"
    And a key-based shadow mock "S1"
    When I send a "POST" request to "/payments" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 503

  Scenario: A session override and the global stub it hides have separate counters
    Given a key-based shadow mock "S1"
    And the global mapping:
      """json
      { "id": "ffffffff-0000-0000-0000-000000000001", "request": { "urlPath": "/fx" }, "response": { "status": 200 },
        "callResponses": [ { "calls": "1", "response": { "status": 201 } } ] }
      """
    And the mapping in "S1":
      """json
      { "id": "ffffffff-0000-0000-0000-000000000001", "request": { "urlPath": "/fx" }, "response": { "status": 200 },
        "callResponses": [ { "calls": "1", "response": { "status": 202 } } ] }
      """
    When I send a "GET" request to "/fx" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 202
    When I send a "GET" request to "/fx"
    Then the response status is 201

  Scenario: Session counters are listed and reset through the session scope
    Given a key-based shadow mock "S1"
    And I send 2 "POST" requests to "/payments" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    When I call admin "GET" "/sessions/${S1.id}/counters"
    Then the response JSON at "$.counters[0].count" is 2
    And the response JSON at "$.counters[0].stubLayer" is "global"
    When I call admin "POST" "/sessions/${S1.id}/counters/reset"
    And I send a "POST" request to "/payments" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 503

  Scenario: A nested key-based shadow mock has its own counters
    Given a URL-based shadow mock "U1"
    And a key-based shadow mock "K1" with:
      """json
      { "parentId": "${U1.id}" }
      """
    And I send a "POST" request to "/payments" via "U1"
    When I send a "POST" request to "/payments" via "U1" with header "X-Shadow-Mock-Key" set to "${K1.key}"
    Then the response status is 503

  Scenario: Counters are discarded with the session
    Given a key-based shadow mock "S1" with:
      """json
      { "ttlSeconds": 60 }
      """
    And I send a "POST" request to "/payments" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    When the test clock advances by "2m"
    Then the store holds no call counters for "S1"

  @nfr
  # Needs response templating ({{request.callNumber}}).
  @P3
  Scenario: Concurrent sessions each get gap-free call numbers
    Given the global mapping:
      """json
      { "request": { "urlPath": "/seq" }, "response": { "status": 200, "body": "{{request.callNumber}}", "transformers": ["response-template"] } }
      """
    And 50 key-based shadow mocks
    When each session concurrently sends 20 "GET" requests to "/seq"
    Then every session received exactly the numbers 1 to 20
