@saas @management-app @P5 @FR-9.3b @security
Feature: CI token exchange endpoint
  CI swaps a CI key for a short-lived Clerk M2M JWT using the OAuth2
  client-credentials shape.

  Background:
    Given the org "acme" with handle "acme" on plan "Team"
    And the CI key "ck_ACME0000000000000" with secret "cks_SECRET" exists in org "acme"
    And the test clock is at "2026-11-01T09:00:00Z"

  Scenario: Exchange with HTTP Basic credentials
    When CI posts "grant_type=client_credentials" to "/api/ci/token" with basic auth "ck_ACME0000000000000" / "cks_SECRET"
    Then the response status is 200
    And the response JSON at "$.token_type" is "Bearer"
    And the response JSON at "$.expires_in" is 3600
    And the access token is a Clerk-signed JWT with claims:
      | claim     | value                 |
      | org_id    | the acme org id       |
      | org_role  | org:member            |
      | ci_key_id | ck_ACME0000000000000  |

  Scenario: Exchange with form-field credentials
    When CI posts "grant_type=client_credentials&client_id=ck_ACME0000000000000&client_secret=cks_SECRET" to "/api/ci/token"
    Then the response status is 200

  Scenario Outline: Failures use OAuth2 error codes and reveal nothing
    When CI exchanges "<client_id>" / "<secret>" with grant type "<grant>"
    Then the response status is <status>
    And the response JSON at "$.error" is "<error>"

    Examples:
      | client_id            | secret      | grant              | status | error                  |
      | ck_ACME0000000000000 | wrong       | client_credentials | 401    | invalid_client         |
      | ck_NOPE0000000000000 | cks_SECRET  | client_credentials | 401    | invalid_client         |
      | ck_ACME0000000000000 | cks_SECRET  | password           | 400    | unsupported_grant_type |

  Scenario: Tokens are reused while they have more than 10 minutes left
    Given CI exchanged the key at "09:00" and received token "T1"
    When CI exchanges the key again at "09:45"
    Then it receives token "T1" again
    And Clerk minted no new token
    When CI exchanges the key again at "09:51"
    Then it receives a new token
    And Clerk minted exactly one new token

  Scenario: Exchange respects the org IP allowlist when applyToAdmin is on
    Given the org "acme" IP allowlist is "203.0.113.0/24" with "applyToAdmin" on
    When CI exchanges the key from IP "198.51.100.9"
    Then the response status is 403

  Scenario: Exchange is rate limited per key and per IP
    When CI exchanges the key 61 times within a minute
    Then at least 1 exchange receives status 429

  Scenario: Repeated bad secrets trigger back-off
    When 10 exchanges with a wrong secret arrive from one IP within a minute
    Then later exchanges from that IP are delayed or rejected with 429

  Scenario: Every exchange is audit-logged
    When CI exchanges the key successfully from IP "203.0.113.4"
    Then the audit log records key "ck_ACME0000000000000", IP "203.0.113.4" and result "success"
