@saas @multi-instance @P5 @FR-11.7 @NFR-4.1 @TA-10
Feature: Any instance answers any admin call
  Managed mode runs several instances behind a load balancer. The session registry, call counters, hot journal,
  stats and recordings in progress live in Redis, so an admin call to any instance returns the whole org's view.

  Background:
    Given Shadow Mock is running in managed mode with 3 instances and a shared Redis
    And the org "acme" with handle "acme" on plan "Team"

  Scenario: A shadow mock made through one instance is visible through another
    When I create a key-based shadow mock "S1" through instance 1
    Then instance 2 lists "S1"
    And a request carrying its key sent to instance 3 is served from "S1"

  Scenario: Call counters advance across instances
    Given a mapping with call responses "1 -> 503, 2 -> 200" in "S1"
    When the SUT sends the first request through instance 1 and the second through instance 2
    Then the first answer is 503 and the second is 200
    And instance 3 reports the counter as 2

  Scenario: The journal is the same on every instance
    When the SUT sends 3 requests through instance 1 and 2 through instance 2
    Then instance 3 lists 5 requests for the org

  Scenario: A recording started on one instance can be stopped through another
    When I start a recording through instance 1
    Then instance 2 reports the recording as running
    When I stop the recording through instance 3
    Then the recorded mappings include requests the SUT sent through all three instances

  Scenario: Live stats are aggregated
    When the SUT sends 10 requests through each instance
    And I call admin "GET" "/stats/live" through instance 2
    Then the stats show 30 requests

  Scenario: Redis requires authentication and TLS
    When an instance is started with a Redis address without TLS or a password
    Then it refuses to start and says so

  Scenario: Redis being down is said plainly
    Given Redis becomes unreachable
    When I call admin "GET" "/sessions" through instance 1
    Then the response status is 503
    And the response is not a partial list
    And readiness reports "degraded"

  Scenario: Org data is separate in Redis
    Given the org "globex" with handle "globex" on plan "Team"
    When a shadow mock is made for each org
    Then each org's keys in Redis are prefixed with its own org id
    And a credential for org "acme" cannot read org "globex"'s counters
