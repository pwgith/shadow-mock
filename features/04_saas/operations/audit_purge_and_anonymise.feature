@saas @P5 @FR-11.15 @FR-11.5 @security
Feature: The audit trail, purging an org and anonymising a person
  Deleting an org or a person is a job the platform sends to the management app, which calls these operations. They
  must be repeatable, because the platform retries.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"

  Scenario: State-changing admin calls are recorded
    Given I present a token for "dana" in org "acme" with app role "member"
    When I call admin "POST" "/sessions/key-based" with body "{}"
    Then the audit trail has an entry with the org, issuer, "dana", the token's id, the route, the status and the time

  Scenario: A key's use is recorded by its label
    Given a Tool app key "ci-tests" for org "acme" with scopes "sessions,read"
    When I call admin "DELETE" "/sessions/S1" with the key "ci-tests"
    Then the audit trail has an entry for "ci-tests"

  Scenario: Reads are not recorded
    When I call admin "GET" "/sessions" with a token for "dana" in org "acme"
    Then the audit trail has no new entry

  Scenario: Purging an org removes its runtime state
    Given the org has 3 shadow mocks, call counters, 200 journal entries, stats rows and a recording
    When the management app calls admin "POST" "/org/purge" for org "acme"
    Then the response status is 202
    And the shadow mocks, Redis keys, journal rows and stats rows for org "acme" are deleted
    And the purge reports its progress until it is done

  Scenario: Purging is repeatable
    Given the org was purged
    When the management app calls admin "POST" "/org/purge" for org "acme" again
    Then the response status is 200
    And the purge reports "done"

  Scenario: A purged org answers 410
    Given the management app marked the org as deleted and purged it
    When the SUT sends "GET /acme/hello"
    Then the response status is 410

  Scenario: Only the management app can purge, and only its own org
    When I call admin "POST" "/org/purge" with a token for "alice" in org "acme" with app role "member"
    Then the response status is 403
    When I call admin "POST" "/org/purge" with a Tool app key
    Then the response status is 403
    When I call admin "POST" "/org/purge" for org "globex" with a token of scope "system" in org "acme"
    Then the response status is 403

  Scenario: Anonymising a person keeps the record and removes the name
    Given "dana" created 2 shadow mocks and made 5 audited calls
    When the management app calls admin "POST" "/people/dana/anonymise"
    Then the audit trail still has the 5 entries
    And their subject is an anonymous marker
    And the 2 shadow mocks show the same anonymous marker as their creator
    And no entry names "dana"

  Scenario: Anonymising is repeatable and leaves others alone
    Given "dana" has been anonymised
    When the management app calls admin "POST" "/people/dana/anonymise" again
    Then the response status is 200
    And entries for "erin" are unchanged

  Scenario: Journal entries are removed with the org, not traced to a person
    When the management app purges the org
    Then no journal entry for org "acme" is readable
