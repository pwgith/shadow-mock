@saas @P5 @FR-11.8 @FR-11.3 @FR-9.8 @TA-12
Feature: The engine's side of the shared database
  The engine owns the `engine` schema and its migrations. The management app writes configuration tables there and
  never runs a migration. A separate `manage` schema is the management app's own, and the engine cannot reach it.

  Background:
    Given a Postgres database migrated by the engine's migrations

  Scenario: The migrations create what the management app and the engine rely on
    Then the `engine` schema has tables for orgs, mappings, files, settings and API keys
    And it has a `schema_version` table with the current version
    And it has a key-usage table separate from the API keys table

  Scenario Outline: A change to a configuration table notifies the engine
    When a row is <change> in "engine.<table>"
    Then a notification is sent when the transaction commits
    And it names the org and the kind of change

    Examples:
      | change   | table     |
      | inserted | orgs      |
      | updated  | orgs      |
      | inserted | mappings  |
      | updated  | mappings  |
      | deleted  | mappings  |
      | inserted | files     |
      | deleted  | files     |
      | updated  | settings  |
      | inserted | api_keys  |
      | updated  | api_keys  |

  Scenario: Nothing is sent before the transaction commits
    When a transaction changes 20 mappings and then rolls back
    Then no notification is sent

  Scenario: Recording a key's use never causes a reload
    When a row is updated in the key-usage table
    Then no notification is sent

  Scenario: Roles keep the two schemas apart
    Then the engine's database role cannot read or write anything in `manage`
    And the management app's role can write only the named configuration tables in `engine`
    And the management app's role has no right to alter the schema

  Scenario: The schema version is readable
    When the management app reads "engine.schema_version"
    Then it sees the version the engine was built with

  Scenario: The engine refuses a database that is ahead of it
    Given the database's schema version is newer than the engine supports
    When the engine starts
    Then it refuses to start and says which versions it supports

  Scenario: The engine refuses a database that has not been migrated
    Given the database has no `schema_version`
    When the engine starts
    Then it refuses to start and says to run the migrations

  Scenario: Migrations can be run again
    When the migrations are run twice
    Then the second run changes nothing

  Scenario: Row-level security keeps each org to its own rows
    Given the management app sets its org for a transaction
    When it selects from "engine.mappings"
    Then it sees only that org's rows
    And a statement without an org setting sees no rows

  Scenario: Unknown fields in a stored mapping survive
    When the management app stores a mapping with a field the engine does not know
    Then the engine serves the mapping and returns the field unchanged when asked
