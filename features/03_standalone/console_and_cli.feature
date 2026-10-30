@standalone @wip @FR-11.17 @TA-7 @TA-8
Feature: The embedded console and the command line
  For standalone and central-server use the binary can serve a small web console and offers sub-commands over the
  admin API. Both are clients of the API and hold no behaviour of their own, so what they show is what a curl
  against the API shows. Neither exists in managed mode (see managed_mode_guards.feature).

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
    And a request that matched nothing
    When I open the console's journal and select the unmatched entry
    Then it shows the request, the response and the closest mappings with why each missed

  Scenario: The console lists, filters and deletes shadow mocks
    Given Shadow Mock is running with the console enabled
    And 3 shadow mocks
    When I open the console's shadow mocks and delete one
    Then 2 remain
    And the same two are listed by the admin API

  Scenario: The console can dry-run a request
    Given Shadow Mock is running with the console enabled
    When I dry-run "GET /claims/1" in the console
    Then it shows which layer and mapping would serve it and the rendered response
    And nothing is journalled

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
    When I run "shadowmock <command>"
    Then the same call was made to the admin API
    And the output is the API's answer

    Examples:
      | command                                        |
      | session create --kind key-based --ttl 10m      |
      | session list --state active                    |
      | session delete S1                              |
      | journal tail --unmatched                       |
      | recording start --target http://localhost:9000 |
      | recording stop                                 |

  Scenario: In managed mode the command line takes a key from the environment
    Given the environment variable "SHADOWMOCK_KEY" is a Tool app key
    When I run "shadowmock session create --url https://mocks.kaizentis.xyz/acme"
    Then the call carries the key as a bearer token

  Scenario: A command adds nothing the API lacks
    Then every sub-command maps to one documented admin API operation
