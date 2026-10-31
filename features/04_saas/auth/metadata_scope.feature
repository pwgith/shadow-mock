@managed @P5 @FR-11.4 @security @TA-13
Feature: The metadata scope for support
  A support "view as" session reaches the engine with a token carrying `scope: metadata`. It can see how an org is
  set up and nothing it holds: no bodies, no mapping or file content, no dry-run output, no near-miss detail, no
  exports, and no changes.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"
    And a mapping "claims-get" in the global layer whose response body is "secret payload"
    And a key-based shadow mock "S1" that served 3 requests, one with a request body "card=4111"
    And I present a token for "sam" in org "acme" with app role "viewer" and scope "metadata"

  Scenario: The session list shows metadata only
    When I call admin "GET" "/sessions"
    Then the response status is 200
    And each session shows its kind, name, creator, expiry and request count
    And the response contains no mapping bodies

  Scenario: Journal entries have no bodies
    When I call admin "GET" "/requests"
    Then the response status is 200
    And each entry shows its method, path, status, layer and time
    And no entry has a request body, a response body or headers' values

  Scenario Outline: Routes that would return content are refused
    When I call admin "<method>" "<path>"
    Then the response status is 403
    And the response JSON at "$.error" is "scope_metadata"

    Examples:
      | method | path                                  |
      | GET    | /mappings/claims-get                  |
      | GET    | /mappings                             |
      | GET    | /files/payload.json                   |
      | GET    | /requests/REQUEST-ID                  |
      | POST   | /requests/find                        |
      | POST   | /requests/export                      |
      | GET    | /requests/unmatched/near-misses       |
      | POST   | /near-misses/request                  |
      | POST   | /mappings/dry-run                     |
      | GET    | /sessions/S1/mappings                 |

  Scenario Outline: Nothing can be changed
    When I call admin "<method>" "<path>" with body "{}"
    Then the response status is 403
    And the response JSON at "$.error" is "scope_metadata"

    Examples:
      | method | path                  |
      | POST   | /sessions/key-based   |
      | DELETE | /sessions/S1          |
      | POST   | /mappings             |
      | POST   | /settings             |
      | DELETE | /requests             |
      | POST   | /recordings/start     |
      | PUT    | /counters/claims-get  |

  Scenario: Every admin route has a defined behaviour under the scope
    When I call every admin route with the scope "metadata"
    Then each answer is either metadata only or a 403 "scope_metadata"
    And no route is missing from the scope table

  Scenario: A route added without a defined scope is refused
    Given an admin route that the scope table does not list
    When I call it with the scope "metadata"
    Then the response status is 403
