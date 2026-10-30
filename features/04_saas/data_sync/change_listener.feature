@saas @P5 @FR-11.8 @FR-9.8 @NFR-2.4 @TA-12
Feature: A configuration change is never missed
  The management app writes configuration to the `engine` schema. Triggers notify the engine, which reloads. A
  notification can be lost, so the engine also polls a version number.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"

  Scenario: A committed change is served within a second
    When the management app commits a new mapping "fresh" for org "acme"
    Then within 1 second "GET /acme/fresh" is served

  Scenario: A transaction is seen whole or not at all
    When the management app commits one transaction that replaces 20 mappings
    Then the engine reloads once
    And no request sees a mix of the old and the new mappings

  Scenario: The notification names the org and the kind of change
    When the management app changes the settings of org "acme"
    Then the engine reloads only the settings of org "acme"
    And other orgs are not reloaded

  Scenario: The listener uses a session-mode connection
    Then the engine's listener connection is not through a transaction pooler

  Scenario: Reconnecting reloads everything
    Given the listener connection is lost
    And the management app commits a new mapping "missed" for org "acme"
    When the listener reconnects
    Then the engine reloads everything for every loaded org
    And "GET /acme/missed" is served

  Scenario: A missed notification is repaired within 30 seconds
    Given notifications are not being delivered
    When the management app commits a new mapping "slow" for org "acme"
    And 30 seconds pass
    Then "GET /acme/slow" is served

  Scenario: Readiness says when the listener is down
    Given the listener connection is lost
    When I request "/__ready"
    Then the response status is 200
    And the response JSON at "$.status" is "degraded"
    And the response JSON at "$.checks.listener" is "down"

  Scenario: A suspension or a revoked key is never missed
    Given notifications are not being delivered
    When the management app sets the status of org "acme" to "suspended"
    And 30 seconds pass
    Then "GET /acme/hello" is answered 403

  Scenario: Orgs are loaded when first needed
    Given the engine has just started
    When the first request for org "acme" arrives
    Then the engine loads org "acme" and serves the request
    And orgs that have had no request are not in memory

  Scenario: A database outage keeps the last configuration
    Given the database becomes unreachable
    When the SUT sends "GET /acme/hello"
    Then the request is served from the last configuration
    And readiness reports "degraded"

  Scenario: The engine's schema version is reported
    When I call admin "GET" "/version"
    Then the response has "schemaVersion" and "mappingSchema"
