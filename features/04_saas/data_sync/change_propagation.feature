@saas @multi-instance @P5 @FR-9.8 @NFR-2.4 @NFR-3.3 @NFR-4.1
Feature: Change propagation from the shared database to every engine instance
  The management app writes straight to Postgres. Triggers notify one leader
  instance, which fans the change out over Redis. A 30-second reconciliation
  is the safety net.

  Background:
    Given Shadow Mock is running in SaaS mode with 3 instances
    And the org "acme" with handle "acme" on plan "Team"

  Scenario: A mapping written directly to the database is served by every instance
    When a global mapping for "/db-write" returning 200 is inserted directly into the database for org "acme"
    Then within 1 s instances 1, 2 and 3 all return 200 for "GET /db-write"

  Scenario: Deleting a mapping in the database removes it everywhere
    Given org "acme" has the global mapping:
      """json
      { "request": { "urlPath": "/gone" }, "response": { "status": 200 } }
      """
    When the mapping for "/gone" is deleted directly from the database
    Then within 1 s instances 1, 2 and 3 all return 404 for "GET /gone"

  Scenario: Session creation is read-your-writes on the creating instance
    Given I am "alice" in org "acme" with role "org:member"
    When I create a key-based shadow mock "S1" through instance 1
    Then a request with key "${S1.key}" to instance 1 immediately uses "S1"
    And within 100 ms a request with key "${S1.key}" to instance 3 uses "S1"

  Scenario: A lost notification is repaired by reconciliation
    Given notifications are dropped for the next change
    When a global mapping for "/silent" returning 200 is inserted directly into the database for org "acme"
    Then within 35 s every instance returns 200 for "GET /silent"

  Scenario: Leadership fails over when the leader instance dies
    Given instance 1 holds the LISTEN leadership
    When instance 1 is killed
    Then within 15 s another instance holds the LISTEN leadership
    And a mapping inserted directly into the database is served within 1 s afterwards

  Scenario: Org settings and allowlist changes propagate the same way
    When the org "acme" IP allowlist is changed directly in the database
    Then within 1 s every instance enforces the new allowlist

  Scenario: Orgs are loaded lazily and evicted when idle
    Given 500 orgs exist
    When only org "acme" receives traffic
    Then each instance has loaded snapshots for org "acme" only
    When org "acme" receives no traffic for 31 minutes
    Then the instances have evicted org "acme"
    And the next request for org "acme" is served correctly after a single load

  Scenario: Concurrent first requests for an org load it only once
    Given org "acme" is not loaded on instance 1
    When 100 requests for org "acme" arrive at instance 1 at the same moment
    Then org "acme" was loaded exactly once on instance 1
