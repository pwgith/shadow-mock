@saas @management-app @P5 @FR-9.3b
Feature: CI keys in the management app

  Background:
    Given the org "acme" with handle "acme" on plan "Team"
    And I am signed in to the management app as "carol" in org "acme" with role "org:admin"

  Scenario: Create a CI key and see the secret only once
    When I create a CI key labelled "claims-api GitHub Actions"
    Then I see a client id matching "^ck_[0-9A-Z]{16}$"
    And I see a client secret matching "^cks_[0-9A-Z]{52}$"
    When I reload the CI keys page
    Then the secret is no longer shown
    And the database stores only a SHA-256 hash of the secret

  Scenario: Revoke a key
    Given a CI key "ck_ACME0000000000000"
    When I revoke it
    Then it is shown as revoked
    And exchanging it fails with "invalid_client"

  Scenario: Rotate a key without downtime
    Given a CI key "ck_OLD0000000000000"
    When I create a replacement key and update CI to use it
    And I revoke "ck_OLD0000000000000"
    Then CI jobs using the new key keep working

  Scenario: Last-used details are shown
    Given the CI key "ck_ACME0000000000000" was exchanged from IP "203.0.113.4"
    When I open the CI keys page
    Then I see "last used" and "203.0.113.4" for that key

  Scenario Outline: Who can manage CI keys
    Given I am signed in to the management app as "<user>" in org "acme"
    When I create a CI key labelled "x"
    Then the result is "<result>"

    Examples:
      | user                                  | result     |
      | carol (org:admin)                     | created    |
      | dana (team with ci-keys:manage)       | created    |
      | erin (org:member, no grant)           | forbidden  |

  Scenario: An expiry reminder is shown for old keys
    Given a CI key created 12 months ago
    When I open the CI keys page
    Then I see a rotation reminder for that key
