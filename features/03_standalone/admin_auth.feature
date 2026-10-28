@standalone @central-server @P4 @FR-9.2 @NFR-5.2
Feature: Admin tokens on the central server
  A shared server puts the admin API behind bearer tokens. Each token has a role.
  Mock traffic never needs one.

  Background:
    Given Shadow Mock is running with configuration:
      """yaml
      auth:
        driver: token
        tokens:
          - { name: ops, role: admin, token: "admin-token-0123456789" }
          - { name: ci, role: editor, token: "editor-token-0123456789" }
          - { name: dash, role: viewer, token: "viewer-token-0123456789" }
          - { name: hashed, role: viewer, tokenSha256: "b22fd458dbe2426bebe59cfafe4c46e013df3efd439e4d91cda767ac3052ddd0" }
      """

  Scenario: The admin API refuses a call with no token
    When I use no admin token
    And I call admin "GET" "/mappings"
    Then the response status is 401
    And the response JSON at "$.error" is "invalid_token"
    And the response header "WWW-Authenticate" contains "Bearer"

  Scenario: The admin API refuses a token it does not know
    Given I use the admin token "not-a-real-token-0123456"
    When I call admin "GET" "/mappings"
    Then the response status is 401
    And the response JSON at "$.error" is "invalid_token"

  Scenario: A token must be sent as a bearer token
    When I call admin "GET" "/mappings" with header "Authorization" set to "Basic YWRtaW4tdG9rZW4tMDEyMzQ1Njc4OQ=="
    Then the response status is 401

  Scenario: A viewer can read but not change anything
    Given I use the admin token "viewer-token-0123456789"
    When I call admin "GET" "/mappings"
    Then the response status is 200
    When I call admin "POST" "/sessions/key-based" with:
      """json
      { "name": "s" }
      """
    Then the response status is 403
    And the response JSON at "$.error" is "forbidden"

  Scenario: An editor works with sessions but not with the global mappings
    Given I use the admin token "editor-token-0123456789"
    And a key-based shadow mock "S1"
    And the mapping in "S1":
      """json
      { "request": { "urlPath": "/mine" }, "response": { "status": 200, "body": "session" } }
      """
    Then a "GET" request to "/mine" with key "${S1.key}" returns body "session"
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/global" }, "response": { "status": 200 } }
      """
    Then the response status is 403
    When I call admin "POST" "/reset"
    Then the response status is 403

  Scenario: An admin changes the global mappings
    Given I use the admin token "admin-token-0123456789"
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/global" }, "response": { "status": 200, "body": "global" } }
      """
    Then the response status is 201
    And a "GET" request to "/global" returns body "global"

  Scenario: A token kept only as its hash works
    Given I use the admin token "hashed-token-0123456789"
    When I call admin "GET" "/mappings"
    Then the response status is 200

  Scenario: Mock traffic needs no token
    Given I use the admin token "admin-token-0123456789"
    And the global mapping:
      """json
      { "request": { "urlPath": "/open" }, "response": { "status": 200, "body": "served" } }
      """
    When I use no admin token
    Then a "GET" request to "/open" returns body "served"

  Scenario Outline: Health checks need no token
    When I use no admin token
    Then a "GET" request to "<path>" returns status 200

    Examples:
      | path             |
      | /__health        |
      | /__ready         |
      | /__admin/health  |

  Scenario: The metrics need a token
    When I use no admin token
    Then a "GET" request to "/__metrics" returns status 401
    When I use the admin token "viewer-token-0123456789"
    And I scrape "/__metrics"
    Then the metrics include:
      | metric                      |
      | shadowmock_active_sessions  |

  Scenario: A session key does not open the admin API
    Given I use the admin token "editor-token-0123456789"
    And a key-based shadow mock "S1"
    When I use no admin token
    And I call admin "GET" "/sessions" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 401

  Scenario: Server mode will not start without an admin token
    Given the configuration file contains:
      """yaml
      mode: server
      """
    When Shadow Mock starts
    Then start-up fails with an error mentioning "mode: server needs auth.driver: token"

  Scenario: Tokens without the driver would leave the admin API open, so start-up fails
    Given the configuration file contains:
      """yaml
      auth:
        tokens:
          - { name: ops, role: admin, token: "admin-token-0123456789" }
      """
    When Shadow Mock starts
    Then start-up fails with an error mentioning "auth.driver is not token"

  Scenario: A token that is too short fails start-up
    Given the configuration file contains:
      """yaml
      auth:
        driver: token
        tokens:
          - { name: ops, role: admin, token: "short" }
      """
    When Shadow Mock starts
    Then start-up fails with an error mentioning "at least 16 characters"
