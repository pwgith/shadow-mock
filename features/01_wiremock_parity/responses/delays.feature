@wiremock @P3 @FR-3.2
Feature: Response delays

  Background:
    Given Shadow Mock is running

  Scenario: Fixed delay
    Given the global mapping:
      """json
      { "request": { "urlPath": "/slow" }, "response": { "status": 200, "fixedDelayMilliseconds": 500 } }
      """
    When I send a "GET" request to "/slow"
    Then the response arrives after at least 500 ms

  Scenario Outline: Random delay distributions stay within their bounds
    Given the global mapping:
      """json
      { "request": { "urlPath": "/rand" }, "response": { "status": 200, "delayDistribution": <distribution> } }
      """
    When I send 50 "GET" requests to "/rand"
    Then every response arrives after between <min> and <max> ms

    Examples:
      | distribution                                              | min | max  |
      | { "type": "uniform", "lower": 100, "upper": 200 }         | 100 | 250  |
      | { "type": "lognormal", "median": 80, "sigma": 0.1 }       | 40  | 400  |

  Scenario: Chunked dribble delay spreads the body over time
    Given the global mapping:
      """json
      { "request": { "urlPath": "/dribble" },
        "response": { "status": 200, "body": "0123456789", "chunkedDribbleDelay": { "numberOfChunks": 5, "totalDuration": 1000 } } }
      """
    When I send a "GET" request to "/dribble"
    Then the body arrives in 5 chunks over at least 1000 ms

  @standalone
  Scenario: Global fixed delay set through the settings API
    Given I call admin "POST" "/settings" with:
      """json
      { "fixedDelay": 300 }
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/any" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/any"
    Then the response arrives after at least 300 ms

  Scenario: A client that disconnects during a delay frees the request
    Given the global mapping:
      """json
      { "request": { "urlPath": "/long" }, "response": { "status": 200, "fixedDelayMilliseconds": 10000 } }
      """
    When I send a "GET" request to "/long" and disconnect after 100 ms
    Then within 500 ms the engine reports 0 in-flight requests for "/long"

  @saas
  Scenario: Delays above the platform maximum are rejected
    When I create a session mapping with "fixedDelayMilliseconds" of 600000
    Then the response status is 422
    And the response JSON at "$.errors[0].detail" contains "maximum delay"
