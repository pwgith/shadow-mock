@saas @management-app @P5 @FR-11.11 @FR-11.10 @NFR-2.4 @TA-3 @TA-6
Feature: What the management app needs from the admin API
  The management app's shadow mock, journal and stats pages are clients of the engine's admin API, called from its
  server with a token it signs for the user. These are the parts of the API it relies on beyond WireMock's.

  Background:
    Given Shadow Mock is running in managed mode with the trusted issuer "management-app"
    And the org "acme" with handle "acme" on plan "Team"
    And I present a token for "erin" in org "acme" with app role "member"

  Scenario Outline: The session list can be filtered
    Given shadow mocks:
      | name | kind      | createdBy | createdByKey | state   |
      | a    | key-based | erin      |              | active  |
      | b    | url-based | dana      |              | active  |
      | c    | key-based |           | ci-tests     | active  |
      | d    | key-based | erin      |              | expired |
    When I call admin "GET" "/sessions?<query>"
    Then the sessions listed are "<names>"

    Examples:
      | query                  | names   |
      | kind=url-based         | b       |
      | state=active           | a, b, c |
      | state=expired          | d       |
      | createdBy=erin         | a, d    |
      | createdByKey=ci-tests  | c       |
      | name=a                 | a       |
      | kind=key-based&state=active | a, c |

  Scenario: The session list is paged, newest first
    Given 120 shadow mocks
    When I call admin "GET" "/sessions?limit=50"
    Then 50 sessions are listed, newest first
    And the response has a cursor for the next page
    And following the cursor gives the next 50

  Scenario Outline: The journal can be searched
    Given journal entries:
      | method | path        | status | layer     | session | matched |
      | GET    | /claims/1   | 200    | global    |         | true    |
      | POST   | /claims/2   | 404    | global    |         | false   |
      | GET    | /orders/9   | 200    | key-based | S1      | true    |
      | GET    | /orders/10  | 500    | url-based | S2      | true    |
    When I call admin "GET" "/requests?<query>"
    Then the entries listed are "<paths>"

    Examples:
      | query                       | paths                |
      | status=404                  | /claims/2            |
      | method=POST                 | /claims/2            |
      | path=/orders                | /orders/10, /orders/9 |
      | session=S1                  | /orders/9            |
      | layer=url-based             | /orders/10           |
      | matched=false               | /claims/2            |
      | matched=true&method=GET     | /orders/10, /orders/9, /claims/1 |

  Scenario: The journal can be searched by time, newest first, with a cursor
    Given 1000 journal entries over 2 hours
    When I call admin "GET" "/requests?from=<hour 2>&limit=100"
    Then only entries from that hour are listed, newest first
    And the response has a cursor for the next page

  Scenario: The journal answers quickly over many rows
    Given 500000 journal entries
    When I search by status and time with a limit of 100
    Then the answer arrives within 1 second at the 95th percentile

  Scenario: An entry says why it has no body
    Given the org stores no journal bodies
    When I open a journal entry
    Then the response says bodies are not stored because of the org's journal settings
    Given the org stores bodies and a body was 300 KiB
    When I open that entry
    Then the response says the body was truncated at 256 KiB

  Scenario: A journal entry and near-miss analysis name the owning set
    Given a mapping "claims-get" with metadata source "claims-api" in the global layer
    When the SUT sends a request that "claims-get" serves
    And I open the journal entry
    Then the entry's "source" is "claims-api"
    When the SUT sends a request that nearly matches "claims-get"
    And I open the near-miss analysis
    Then the candidate "claims-get" shows source "claims-api"

  Scenario: A mapping without a source has no label and works the same
    Given a mapping "plain" with no metadata in the global layer
    When the SUT sends a request that "plain" serves
    Then the entry has no "source"

  Scenario: Stats have a history and a live view
    Given the org has served traffic for 3 hours
    When I call admin "GET" "/stats?from=<3 hours ago>&to=<now>&step=1h"
    Then there is a point for each hour with requests by layer, match rate, top unmatched paths, latency p50 and p99, detection stage hits, active sessions and expiries
    And the IP rejections by source list the top 20
    When I call admin "GET" "/stats/live"
    Then it shows the present minute

  Scenario: Journal settings are the org's own
    When the management app sets the org's journal capture to "sessions-and-unmatched" and body storage off
    Then within 1 second a matched global request is not journalled
    And an unmatched request is journalled without a body
    And a request to a shadow mock is journalled without a body

  Scenario: Response compression can be turned off for an org
    When the management app turns response compression off for the org
    Then a response to a client that accepts gzip is not compressed

  Scenario: Counters can be read, set and reset
    Given a shadow mock "S1" with call responses
    When I set the counter for "claims-get" in "S1" to 2
    Then the next response is the third in the list
    And I can read the counter and reset it

  Scenario: Promoting reads a shadow mock's mappings and files
    Given a shadow mock "S1" with 2 mappings that use 1 file
    When I call admin "GET" "/sessions/S1/mappings" and "GET" "/sessions/S1/files"
    Then I get both mappings and the file's content

  Scenario: A dry run changes nothing and says which layer would serve
    When I dry-run "GET /claims/1" against "S1"
    Then the response says the layer, the mapping and the rendered response
    And the journal and counters are unchanged

  Scenario: The contract is versioned
    When I call admin "GET" "/version"
    Then the response has "version", "schemaVersion" and "mappingSchema"
    And the OpenAPI description and "schema/stub-mapping.schema.json" of that release agree with it

  Scenario: Unknown fields come back unchanged
    When I create a mapping through validate with a field the engine does not know
    Then the answer is valid
    And storing and reading the mapping returns the field unchanged
