@standalone @central-server @P5 @FR-11.17 @TA-7 @TA-8
Feature: The embedded console and the command line
  For standalone and central-server use the binary can serve a small web console and offers sub-commands over the
  admin API. Both are clients of the API and hold no behaviour of their own, so what they show is what a curl
  against the API shows. Neither exists in managed mode (see managed_mode_guards.feature).

  # The harness has no browser. It plays the console as the page's script does: it makes the calls listed in the
  # console's calls.json, in the order the script makes them, and checks the answers hold what the script shows.
  # internal/console tests that the script makes only those calls and builds the page from text only.

  Scenario: The console is served by the binary
    Given Shadow Mock is running with the console enabled
    When I request "/__console/"
    Then the response status is 200
    And the response is a static page that calls only "/__admin"

  Scenario: The console is off unless asked for
    Given Shadow Mock is running
    When I request "/__console/"
    Then the response status is 404

  Scenario: The console shows the journal with near-miss analysis
    Given Shadow Mock is running with the console enabled
    And the global mapping:
      """json
      { "request": { "method": "GET", "urlPath": "/claims/1", "headers": { "Accept": { "equalTo": "application/json" } } },
        "response": { "status": 200 } }
      """
    And a request that matched nothing
    When I open the console's journal and select the unmatched entry
    Then it shows the request, the response and the closest mappings with why each missed

  Scenario: The console lists and deletes shadow mocks
    Given Shadow Mock is running with the console enabled
    And 3 shadow mocks
    When I open the console's shadow mocks and delete one
    Then 2 remain
    And the same two are listed by the admin API

  Scenario: The console can dry-run a request
    Given Shadow Mock is running with the console enabled
    And the global mapping:
      """json
      { "request": { "method": "GET", "urlPath": "/claims/1" }, "response": { "status": 200, "body": "claim one" } }
      """
    When I dry-run "GET /claims/1" in the console
    Then it shows which layer and mapping would serve it and the rendered response
    And nothing is journalled

  Scenario: The console edits a shadow mock
    Given Shadow Mock is running with the console enabled
    And a key-based shadow mock "S1" with:
      """json
      { "ttlSeconds": 86400 }
      """
    When in the console I make the "updateSession" call on "${S1.id}" with:
      """json
      { "name": "renamed", "ttlSeconds": 7200, "addTokens": ["POL-555"] }
      """
    Then the response status is 200
    When I call admin "GET" "/sessions/${S1.id}"
    Then the response JSON at "$.name" is "renamed"
    And the response JSON at "$.tokens[0]" is "POL-555"
    When in the console I make the "updateSession" call on "${S1.id}" with:
      """json
      { "removeTokens": ["POL-555"] }
      """
    And I call admin "GET" "/sessions/${S1.id}"
    Then the response JSON at "$.tokens" is absent

  Scenario: The console keeps a shadow mock alive
    Given Shadow Mock is running with the console enabled
    And the test clock is at "2026-11-01T09:00:00Z"
    And a URL-based shadow mock "U1" with:
      """json
      { "ttlSeconds": 86400 }
      """
    And the test clock advances by "12h"
    When in the console I make the "keepAlive" call on "${U1.id}"
    Then the response status is 200
    And the response JSON at "$.expiresAt" is "2026-11-02T21:00:00Z"

  Scenario: The console shows, sets and resets the global counters
    Given Shadow Mock is running with the console enabled
    And the global mapping:
      """json
      { "id": "eeeeeeee-0000-0000-0000-0000000000c1", "request": { "urlPath": "/k" }, "response": { "status": 200 },
        "callResponses": [ { "calls": "10", "response": { "status": 500 } } ] }
      """
    And I send 2 "GET" requests to "/k"
    When in the console I make the "counters" call
    Then the response JSON at "$.counters[?(@.stubMappingId=='eeeeeeee-0000-0000-0000-0000000000c1')].count" is 2
    When in the console I make the "setCounter" call for the stub "eeeeeeee-0000-0000-0000-0000000000c1" with:
      """json
      { "count": 9 }
      """
    And I send a "GET" request to "/k"
    Then the response status is 500
    When in the console I make the "resetCounter" call for the stub "eeeeeeee-0000-0000-0000-0000000000c1"
    Then admin "GET" "/counters" shows count 0 for stub "eeeeeeee-0000-0000-0000-0000000000c1"
    When I send 3 "GET" requests to "/k"
    And in the console I make the "resetCounters" call
    Then admin "GET" "/counters" shows count 0 for stub "eeeeeeee-0000-0000-0000-0000000000c1"

  Scenario: The console shows, sets and resets a shadow mock's counters
    Given Shadow Mock is running with the console enabled
    And a key-based shadow mock "S1"
    And the global mapping:
      """json
      { "id": "eeeeeeee-0000-0000-0000-0000000000c2", "request": { "urlPath": "/k" }, "response": { "status": 200 },
        "callResponses": [ { "calls": "10", "response": { "status": 500 } } ] }
      """
    And I send 2 "GET" requests to "/k" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    When in the console I make the "sessionCounters" call on "${S1.id}"
    Then the response JSON at "$.counters[0].count" is 2
    When in the console I make the "setSessionCounter" call on "${S1.id}" for the stub "eeeeeeee-0000-0000-0000-0000000000c2" with:
      """json
      { "count": 9, "stubLayer": "global" }
      """
    And I send a "GET" request to "/k" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 500
    When in the console I make the "resetSessionCounters" call on "${S1.id}"
    And I send a "GET" request to "/k" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    Then the response status is 200

  Scenario: The console records into the global session
    Given Shadow Mock is running with the console enabled
    And the upstream stub server "claims" returns 200 with JSON '{"id":1}' for "GET /claims/1"
    When in the console I make the "recordingStatus" call
    Then the response JSON at "$.status" is "NeverStarted"
    When in the console I make the "startRecording" call with:
      """json
      { "targetBaseUrl": "${upstream.claims}" }
      """
    Then the response status is 200
    When in the console I make the "recordingStatus" call
    Then the response JSON at "$.status" is "Recording"
    When I send a "GET" request to "/claims/1"
    And in the console I make the "stopRecording" call
    Then the response JSON at "$.mappings" has 1 items

  Scenario: The console records into a shadow mock
    Given Shadow Mock is running with the console enabled
    And the upstream stub server "claims" returns 200 with JSON '{"id":1}' for "GET /claims/1"
    And a key-based shadow mock "S1"
    When in the console I make the "startSessionRecording" call on "${S1.id}" with:
      """json
      { "targetBaseUrl": "${upstream.claims}" }
      """
    And I send a "GET" request to "/claims/1" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    And in the console I make the "sessionRecordingStatus" call on "${S1.id}"
    Then the response JSON at "$.status" is "Recording"
    When in the console I make the "stopSessionRecording" call on "${S1.id}"
    Then the response JSON at "$.mappings" has 1 items
    And admin "GET" "/mappings" does not include a mapping for "/claims/1"

  Scenario: The console needs no sign-in standalone
    Given Shadow Mock is running with the console enabled
    When I open the console
    Then I am not asked to sign in

  Scenario: The central server's console asks for the admin token
    Given Shadow Mock is running as a central server with admin tokens and the console enabled
    When I open the console without a token
    Then the admin calls it makes are refused with 401

  Scenario Outline: Command-line sub-commands call the admin API
    Given Shadow Mock is running
    And a key-based shadow mock "S1"
    When I run "shadowmock <command>"
    Then the admin API received "<call>"
    And the output is the API's answer

    Examples:
      | command                                        | call                                         |
      | session create --kind key-based --ttl 10m      | POST /__admin/sessions/key-based             |
      | session list --state active                    | GET /__admin/sessions?state=active           |
      | session delete ${S1.id}                        | DELETE /__admin/sessions/${S1.id}            |
      | journal tail --unmatched                       | GET /__admin/requests?limit=20&unmatched=true |
      | recording start --target http://localhost:9000 | POST /__admin/recordings/start               |
      | recording stop                                 | POST /__admin/recordings/stop                |

  Scenario: In managed mode the command line takes a key from the environment
    Given the environment variable "SHADOWMOCK_KEY" is a Tool app key
    When I run "shadowmock session create --url https://mocks.kaizentis.xyz/acme"
    Then the call carries the key as a bearer token

  Scenario: A command adds nothing the API lacks
    Then every sub-command maps to one documented admin API operation
