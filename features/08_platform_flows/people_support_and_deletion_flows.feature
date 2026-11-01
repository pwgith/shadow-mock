@managed @P5 @flow-F1 @flow-F7 @flow-F11 @flow-F12 @flow-F13 @flow-F17 @flow-F30 @flow-F32 @FR-11.4 @FR-11.5 @FR-11.15 @TA-9 @TA-13 @TA-14
Feature: The engine's part of enablement, support, leaving and deletion
  The platform owns sign-up, people and the deletion clock. The engine is told through configuration rows and a few
  admin calls the management app makes.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"

  # F1: a new customer.
  Scenario: F1 - An enabled org is served before anyone opens the management app
    When the management app commits the org "acme" with handle "acme" and a mapping for "GET /hello"
    Then within 1 second "GET /acme/hello" is served
    And the engine was not restarted

  Scenario: F1 - Enabling twice changes nothing
    Given the org "acme" with handle "acme" on plan "Free"
    And a mapping "hello" in the global layer for "GET /hello"
    And a Tool app key "ci" for org "acme" with scopes "sessions,read"
    When the management app writes the same org row again
    Then the org, its mappings and its keys are unchanged

  # F7: org settings reach the engine.
  Scenario: F7 - The IP allowlist is applied within a second
    Given the org "acme" with handle "acme" on plan "Team"
    When the org's IP allowlist is "198.51.100.0/24"
    Then within 1 s a "GET" request to "/acme/hello" from IP "203.0.113.4" returns status 403

  # Not built: the org's journal capture and response compression settings.
  @saas
  Scenario Outline: F7 - Org settings are applied within a second
    Given the org "acme" with handle "acme" on plan "Team"
    When the management app changes <setting>
    Then within 1 second <effect>

    Examples:
      | setting                                              | effect                                                       |
      | journal capture to "off"                             | a served request is not journalled                           |
      | journal capture to "sessions-and-unmatched"          | a matched global request is not journalled                   |
      | response compression to off                          | a response to a client that accepts gzip is not compressed   |

  # F11: moving between orgs.
  Scenario: F11 - A token for one org cannot be used on another's URL
    Given the org "acme" with handle "acme" on plan "Team"
    And the org "client" with handle "client" on plan "Team"
    And I present a token for "erin" in org "client" with app role "member"
    When I call admin "GET" "https://shadowmock.test/acme/__admin/sessions"
    Then the response status is 403
    And the response JSON at "$.error" is "org_mismatch"

  # F12: support helps a customer.
  Scenario: F12 - A support session sees how the org is set up, not what it holds
    Given the org "acme" with handle "acme" on plan "Team"
    And the org has mappings with bodies, 3 shadow mocks and 50 journal entries with request bodies
    And I present a token for "sam" in org "acme" with app role "viewer" and scope "metadata"
    When I call admin "GET" "/sessions"
    Then the response status is 200
    And the response holds none of the org's bodies
    When I call admin "GET" "/requests"
    Then the response status is 200
    And the response holds none of the org's bodies
    When I call admin "GET" "/usage"
    Then the response status is 200
    When I call admin "GET" "/mappings"
    Then the response status is 403
    And the response JSON at "$.error" is "scope_metadata"
    # FR-11.15 audits calls that change state; a support session only reads, so it leaves no entry.
    And the audit trail has no entry for "sam"

  # F13 and F17: deletion, and a person who leaves.
  Scenario: F13 - An org is deleted in the order that keeps it deleted
    Given the org "acme" with handle "acme" on plan "Team"
    And a Tool app key "ci" for org "acme" with scopes "sessions,read"
    And the org has 3 shadow mocks, call counters, 5 journal entries, stats rows and a recording
    When the management app marks org "acme" as deleted
    Then at once the SUT, its keys and its tokens are answered 410
    When the management app calls admin "POST" "/org/purge" for org "acme"
    Then the shadow mocks, Redis keys, journal rows and stats rows for org "acme" are deleted
    And the purge reports "done"
    And the handle "acme" is still reserved

  Scenario: F13 - Nothing the SaaS app sends later can bring a deleted org back
    Given the org "acme" with handle "acme" on plan "Team"
    And the management app marked the org as deleted and purged it
    When a late write sets the status of org "acme" to "active"
    Then the engine still answers 410 for org "acme"

  Scenario: F17 - A person who asked to be forgotten is removed from the record, not the record
    Given the org "acme" with handle "acme" on plan "Team"
    And "dana" created 2 shadow mocks and made 5 audited calls
    When the management app calls admin "POST" "/people/dana/anonymise"
    Then the audit trail still has the 5 entries
    And their subject is an anonymous marker
    And the 2 shadow mocks show the same anonymous marker as their creator
    And no entry names "dana"

  Scenario: F17 - Keys a leaver made keep working until the management app acts
    Given the org "acme" with handle "acme" on plan "Team"
    And a Tool app key "claims-ci" made by "dana" for org "acme"
    When "dana" has been anonymised
    Then the key still authenticates until the management app revokes or disables it

  # F30 and F32: renames and environments.
  Scenario: F30 - Renaming the platform slug changes nothing for the SUT
    Given the org "acme" with handle "acme" on plan "Team" and a mapping for "GET /hello"
    When the platform renames the org's slug to "acme-corp"
    Then "GET /acme/hello" is served

  Scenario: F32 - Separate orgs give separate environments
    Given the org "acme" with handle "acme" on plan "Team" and a mapping "GET /v" returning "production"
    And the org "acme-staging" with handle "acme-staging" on plan "Team" and a mapping "GET /v" returning "staging"
    Then "GET /acme/v" returns "production" and "GET /acme-staging/v" returns "staging"
    And a key for "acme" cannot reach "acme-staging"
