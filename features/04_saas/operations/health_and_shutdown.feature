@saas @P5 @NFR-6.4
Feature: Health, readiness and graceful shutdown on the hosting platform

  Scenario Outline: Readiness depends on the shared cache and database
    Given Shadow Mock is running in SaaS mode
    And <dependency> is <state>
    When the platform calls "GET /__ready"
    Then the response status is <status>

    Examples:
      | dependency    | state       | status |
      | Redis         | reachable   | 200    |
      | Redis         | unreachable | 503    |
      | Postgres      | unreachable | 503    |

  Scenario: Liveness does not depend on downstream services
    Given Shadow Mock is running in SaaS mode
    And Postgres is unreachable
    When the platform calls "GET /__health"
    Then the response status is 200

  Scenario: SIGTERM drains in-flight requests and flushes the journal
    Given Shadow Mock is running in SaaS mode
    And a request to a mapping with a 2 s delay is in flight
    And 50 journal entries are queued
    When the instance receives SIGTERM
    Then it stops accepting new connections
    And the in-flight request completes with status 200
    And the 50 queued journal entries are written
    And the process exits within the platform's grace period

  Scenario: A zero-downtime deploy loses no requests
    Given Shadow Mock is running in SaaS mode with 2 instances
    When the instances are replaced one at a time while 200 requests per second are sent
    Then every request receives a response
