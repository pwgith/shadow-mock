@nfr @perf @P4 @NFR-2.1 @NFR-2.2 @NFR-2.4 @NFR-2.5
Feature: Performance targets
  These run in the benchmark pipeline on a reference machine, not on every
  pull request.

  Scenario: Matching latency with many mappings
    Given Shadow Mock is running on 1 CPU core
    And 5000 global mappings with a realistic mix of matchers
    And a key-based shadow mock with 50 mappings
    When 100000 requests are sent, half with the session key
    Then the added matching latency at p99 is under 2 ms

  Scenario Outline: Detection time is independent of the number of active sessions
    Given <sessions> active key-based shadow mocks, each with 2 match tokens
    When requests with <size> bodies are sent and none contain a key
    Then the p99 detection time is within 10 % of the time with 10 sessions

    Examples:
      | sessions | size    |
      | 1000     | 1 KiB   |
      | 10000    | 1 KiB   |
      | 10000    | 100 KiB |
      | 10000    | 1 MiB   |

  Scenario: Detection time grows linearly with the scanned size
    Given 1000 active key-based shadow mocks
    When requests with bodies of 10 KiB, 100 KiB and 1 MiB are scanned
    Then detection time grows roughly linearly with body size

  Scenario: Session changes never block requests
    Given 10000 active key-based shadow mocks
    And a steady load of 2000 requests per second
    When 100 sessions are created and deleted per second
    Then no request waits on an automaton rebuild
    And every session change is visible within 100 ms

  Scenario: Throughput for simple stubs
    Given Shadow Mock is running on a 2-vCPU instance
    And 100 simple global mappings
    When load is applied for 60 s
    Then the instance sustains at least 10000 requests per second with error rate below 0.1 %

  Scenario: The hot path stays in memory under SaaS load
    Given Shadow Mock is running in SaaS mode with Postgres and Redis
    When 1000 requests per second are served for 60 s
    Then Postgres receives no reads on the request path
    And Redis receives at most 3 round trips per request on average
