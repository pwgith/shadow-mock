@saas @P5 @FR-9.3b
Feature: CI access with short-lived Clerk M2M JWTs (engine side)
  CI exchanges a CI key for a Clerk M2M JWT, at most 60 minutes long, and
  uses it on the engine admin API. Exchange behaviour is specified in
  05_management_app/ci_token_exchange.feature.

  Background:
    Given Shadow Mock is running in SaaS mode
    And the org "acme" with handle "acme" on plan "Team"
    And the CI key "ck_ACME0000000000000" exists in org "acme"

  Scenario: A CI token can manage sessions
    Given I exchange CI key "ck_ACME0000000000000" for a token
    When I call admin "POST" "/sessions/key-based" with:
      """json
      {}
      """
    Then the response status is 201

  Scenario: The token's ci_key_id is recorded on what it creates
    Given I exchange CI key "ck_ACME0000000000000" for a token
    When I call admin "POST" "/sessions/url-based" with:
      """json
      {}
      """
    Then the created session has "createdBy" equal to "ci:ck_ACME0000000000000"

  Scenario: An expired CI token is rejected
    Given I exchange CI key "ck_ACME0000000000000" for a token
    And the test clock advances by "61m"
    When I call admin "GET" "/sessions"
    Then the response status is 401
    And the response JSON at "$.error" is "token_expired"

  Scenario: Revoking the key stops new tokens, and existing tokens expire within an hour
    Given I exchange CI key "ck_ACME0000000000000" for a token
    When the CI key "ck_ACME0000000000000" is revoked
    Then exchanging CI key "ck_ACME0000000000000" fails with "invalid_client"
    And the existing token still works until it expires
    And after the test clock advances by "60m" the existing token is rejected

  Scenario: CI tokens cannot make global writes
    Given I exchange CI key "ck_ACME0000000000000" for a token
    When I call admin "POST" "/mappings" with:
      """json
      { "request": { "urlPath": "/x" }, "response": { "status": 200 } }
      """
    Then the response status is 403
