@saas @P5 @FR-5.6
Feature: Warm journal history in Postgres and object storage
  Each request becomes a lean Postgres row. Bodies under 4 KiB are inline, and
  larger ones go into shared per-instance body objects written every 5
  seconds or at 8 MB.

  Background:
    Given Shadow Mock is running in SaaS mode
    And the org "acme" with handle "acme" on plan "Team"
    And org "acme" has the global mapping:
      """json
      { "request": { "urlPathPattern": "/.*" }, "response": { "status": 200, "body": "ok" } }
      """

  Scenario: A small request is stored inline
    When I send a "POST" request to "/small" with a 1 KB body
    Then within 1 s the journal table has a row for "/small" with an inline request body

  Scenario: A large body goes to a shared object and the row points at it
    When I send a "POST" request to "/large" with a 20 KB body
    Then within 6 s the journal row for "/large" has a body reference with key, offset and length
    And reading that range from object storage and decompressing it gives the original 20 KB body

  Scenario: One body object holds many orgs' bodies, written on time or size
    Given the org "globex" with handle "globex" on plan "Team"
    When instance 1 receives large-body requests for orgs "acme" and "globex" within 1 s
    Then within 6 s exactly 1 new body object has been written by instance 1
    When instance 1 receives 9 MB of large bodies within 1 s
    Then a body object is written as soon as it reaches 8 MB

  Scenario: Rows are inserted only after their body object is written
    Given object storage writes fail
    When I send a "POST" request to "/large" with a 20 KB body
    Then no journal row is inserted for "/large"
    And the metric "journal_dropped_total" increases once the queue limit is reached
    And mock traffic is unaffected

  Scenario: A slow store never blocks mock traffic
    Given Postgres inserts take 5 s each
    When I send 1000 requests
    Then every response arrives within its normal latency
    And excess journal entries are dropped and counted

  Scenario Outline: Retention follows the plan
    Given the org "acme" is on Shadow Mock plan "<plan>"
    And I send a "POST" request to "/old" with a 20 KB body
    When the test clock advances by "<age>"
    And the retention jobs run
    Then the journal row for "/old" <row>
    And its body object is <object>

    Examples:
      | plan     | age | row            | object              |
      | Free     | 2d  | has been deleted | expired by lifecycle |
      | Team     | 29d | still exists   | still present       |
      | Team     | 31d | has been deleted | expired by lifecycle |
      | Business | 89d | still exists   | still present       |

  Scenario Outline: Journal capture settings
    Given the org "acme" has setting "journal.capture" set to "<capture>"
    And a key-based shadow mock "S1"
    When I send a "GET" request to "/matched-global"
    And I send a "GET" request to "/in-session" with key "${S1.key}"
    Then the warm journal holds rows for: <rows>

    Examples:
      | capture                | rows                                |
      | all                    | /matched-global, /in-session        |
      | sessions-and-unmatched | /in-session                         |
      | off                    | none                                |

  Scenario: Body storage can be turned off
    Given the org "acme" has setting "journal.bodies" set to "off"
    When I send a "POST" request to "/secretive" with a 20 KB body
    Then the journal row for "/secretive" has no request or response body
    And no body object contains that body

  Scenario: Row caps per day stop rows but not counting
    Given the org "acme" has a daily journal row cap of 100
    When I send 150 requests today
    Then the warm journal holds 100 rows for today
    And the org "acme" meter "mock_requests" for today is 150
