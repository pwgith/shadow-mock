@managed @P5 @FR-9.3 @FR-9.3a @FR-11.2 @security @TA-9
Feature: Admin calls carry a token from a trusted issuer
  In managed mode the admin API accepts a short-lived token signed by the management app, or a Tool app key
  (tool_app_keys.feature), and nothing else. Trust exists only in the engine's configuration: an issuer name, an
  audience and the location of the issuer's public keys. The engine has no Clerk configuration and never sees a
  launch token.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"

  Scenario: A valid token is accepted and its claims become the principal
    Given I present a token for "alice" in org "acme" with app role "member"
    When I call admin "GET" "/sessions"
    Then the response status is 200

  Scenario Outline: A token is checked in order, and any failure gives no data
    Given I present a token with:
      | claim   | value   |
      | <claim> | <value> |
    When I call admin "GET" "/sessions"
    Then the response status is <status>
    And the response JSON at "$.error" is "<error>"
    And the response contains no mappings, sessions or journal entries

    Examples:
      | claim     | value                              | status | error            |
      | signature | made with an unknown key           | 401    | invalid_token    |
      | alg       | none                               | 401    | invalid_token    |
      | alg       | HS256 using the public key as secret | 401  | invalid_token    |
      | iss       | https://evil.example               | 401    | untrusted_issuer |
      | aud       | the staging environment            | 401    | invalid_audience |
      | exp       | 1 minute ago                       | 401    | token_expired    |
      | lifetime  | 10 minutes                         | 401    | token_too_long   |
      | org_id    | the org "globex"                   | 403    | org_mismatch     |

  Scenario: The lifetime is capped by the engine, whatever the issuer says
    Given the issuer signs a token that expires in 1 hour
    When I call admin "GET" "/sessions"
    Then the response status is 401
    And the response JSON at "$.error" is "token_too_long"

  Scenario: The issuer's keys are re-read, so rotation needs no change
    Given the issuer publishes a new key and signs tokens with it
    When I call admin "GET" "/sessions" with a token signed by the new key
    Then the response status is 200

  Scenario: A pinned public key works in place of a key set
    Given Shadow Mock is configured with the issuer's public key pinned
    And I present a token for "alice" in org "acme" with app role "member"
    When I call admin "GET" "/sessions"
    Then the response status is 200

  Scenario: A token without an org is refused
    Given I present a token with no "org_id" claim
    When I call admin "GET" "/sessions"
    Then the response status is 403
    And the response JSON at "$.error" is "no_active_org"

  Scenario: A token cannot be replayed when replay checking is on
    Given replay checking is on
    And I present a token for "alice" in org "acme" with app role "member"
    When I call admin "GET" "/sessions"
    Then the response status is 200
    When I call admin "GET" "/sessions" with the same token
    Then the response status is 401
    And the response JSON at "$.error" is "token_replayed"

  Scenario: A token from the right issuer for the wrong environment is refused
    Given Shadow Mock expects the audience "mocks-production"
    And I present a token with audience "mocks-staging"
    When I call admin "GET" "/sessions"
    Then the response status is 401

  Scenario Outline: The app role sets what the caller may do
    Given I present a token for "alice" in org "acme" with app role "<role>"
    When I call admin "<method>" "<path>" with body "<body>"
    Then the response status is <status>

    Examples:
      | role   | method | path                 | body | status |
      | viewer | GET    | /sessions            |      | 200    |
      | viewer | POST   | /sessions/key-based  | {}   | 403    |
      | member | POST   | /sessions/key-based  | {}   | 201    |
      | member | POST   | /mappings            | {}   | 403    |
      | admin  | POST   | /mappings            | {}   | 403    |

  Scenario: Mock traffic needs no credential and ignores the SUT's Authorization header
    When the SUT sends "GET /acme/hello" with the header "Authorization: Bearer not-a-token"
    Then the response status is 404
    And the response JSON at "$.error" is not "invalid_token"

  Scenario: Each token's issuer, subject and id are recorded
    Given I present a token for "alice" in org "acme" with app role "member"
    When I call admin "POST" "/sessions/key-based" with body "{}"
    Then the audit trail has an entry with issuer "management-app", subject "alice" and the token's id
