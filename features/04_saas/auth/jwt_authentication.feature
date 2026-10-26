@saas @P5 @FR-9.3 @FR-9.3a
Feature: JWT authentication on the admin API
  The engine accepts only JWTs from trusted Clerk issuers. It never accepts
  opaque API keys.

  Background:
    Given Shadow Mock is running in SaaS mode
    And the org "acme" with handle "acme" on plan "Team"

  Scenario: A valid Clerk session JWT from the shadowmock template is accepted
    Given I am "alice" in org "acme" with role "org:member"
    When I call admin "GET" "/sessions"
    Then the response status is 200

  Scenario Outline: Invalid tokens are rejected
    Given I present a JWT with:
      | claim   | value      |
      | <claim> | <value>    |
    When I call admin "GET" "/sessions"
    Then the response status is 401
    And the response JSON at "$.error" is "<error>"

    Examples:
      | claim     | value                         | error            |
      | signature | signed by an unknown key      | invalid_token    |
      | exp       | 1 minute ago                  | token_expired    |
      | nbf       | in 5 minutes                  | token_not_active |
      | iss       | https://evil.example          | untrusted_issuer |
      | aud       | other-app                     | invalid_audience |
      | alg       | none                          | invalid_token    |

  Scenario: No token at all
    Given I am not authenticated
    When I call admin "GET" "/sessions"
    Then the response status is 401

  Scenario: A token without an active organisation is rejected
    Given I present a JWT for user "alice" with no org claim
    When I call admin "GET" "/sessions"
    Then the response status is 403
    And the response JSON at "$.error" is "no_active_org"

  Scenario: Opaque API keys are never accepted
    Given I send header "Authorization" with value "Bearer sm_live_abcdef123456"
    When I call admin "GET" "/sessions"
    Then the response status is 401

  Scenario: Claim paths for Clerk v2 nested org claims are supported by configuration
    Given the engine is configured with "orgClaim" set to "o.id" and "rolesClaim" set to "o.rol"
    And I present a JWT with:
      | claim | value                         |
      | o     | {"id":"acme_org","rol":"org:member"} |
    When I call admin "GET" "/sessions"
    Then the response status is 200

  Scenario: Signing key rotation is picked up from the JWKS
    Given the fake Clerk issuer rotates to a new signing key
    And I am "alice" in org "acme" with role "org:member"
    When I call admin "GET" "/sessions"
    Then the response status is 200

  Scenario: Mock traffic never needs a JWT and ignores the SUT's Authorization header
    Given org "acme" has the global mapping:
      """json
      { "request": { "urlPath": "/real-dep", "headers": { "Authorization": { "equalTo": "Bearer real-dependency-token" } } }, "response": { "status": 200 } }
      """
    When I send a request:
      | method | path      | header:Authorization          |
      | GET    | /real-dep | Bearer real-dependency-token  |
    Then the response status is 200
