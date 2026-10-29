@wiremock @P3 @standalone @FR-10.13
Feature: Settings WireMock takes as command-line flags
  Every WireMock flag is accepted on the command line (the unit tests in internal/config check the whole
  list against WireMock's own --help), and each one that does something sets a configuration key. These
  scenarios use the keys, so the YAML file and the environment get the same settings. Every expectation
  below was checked against a real WireMock 3.13.2.

  Scenario: Templating can be switched off for every response
    Given Shadow Mock is running with configuration:
      """yaml
      templating:
        disabled: true
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/t" },
        "response": { "status": 200, "body": "{{request.path}}", "transformers": ["response-template"] } }
      """
    When I send a "GET" request to "/t"
    Then the response body equals '{{request.path}}'

  Scenario: Without a journal a count is -1, as in WireMock
    Given Shadow Mock is running with configuration:
      """yaml
      storage:
        journal:
          hot:
            maxEntries: 0
      """
    And the global mapping:
      """json
      { "request": { "urlPath": "/p" }, "response": { "status": 200 } }
      """
    When I send a "GET" request to "/p"
    And I call admin "POST" "/requests/count" with:
      """json
      { "urlPath": "/p" }
      """
    Then the response JSON at "$.count" is -1
    And the response JSON at "$.requestJournalDisabled" is true

  Scenario: The admin API can ask for a username and password
    Given Shadow Mock is running with configuration:
      """yaml
      auth:
        driver: basic
        basic:
          username: ada
          password: s3cret-password
      """
    When I call admin "GET" "/mappings"
    Then the response status is 401
    And the response header "WWW-Authenticate" contains "Basic"
    When I call admin "GET" "/mappings" with header "Authorization" set to "Basic YWRhOnMzY3JldC1wYXNzd29yZA=="
    Then the response status is 200
    When I call admin "GET" "/mappings" with header "Authorization" set to "Basic YWRhOndyb25n"
    Then the response status is 401
    When I call admin "GET" "/health" with header "Authorization" set to "Basic YWRhOnMzY3JldC1wYXNzd29yZA=="
    Then the response status is 200

  Scenario: Mock traffic does not ask for the password
    Given Shadow Mock is running with configuration:
      """yaml
      auth:
        driver: basic
        basic:
          username: ada
          password: s3cret-password
      """
    And I call admin "POST" "/mappings" with header "Authorization" set to "Basic YWRhOnMzY3JldC1wYXNzd29yZA==" and body:
      """json
      { "request": { "urlPath": "/open" }, "response": { "status": 200, "body": "hi" } }
      """
    When I send a "GET" request to "/open"
    Then the response body equals 'hi'

  Scenario: A persisted mapping is named by a filename template
    Given Shadow Mock is running with configuration:
      """yaml
      storage:
        global:
          filenameTemplate: "{{{lower method}}}-{{{id}}}.json"
      """
    When I call admin "POST" "/mappings" with:
      """json
      { "name": "Ignored", "request": { "method": "POST", "urlPath": "/x" }, "response": { "status": 200 }, "persistent": true }
      """
    Then the folder "mappings" has a file named like "post-[0-9a-f-]{36}\.json"

  Rule: Proxying set up the way WireMock's command line does it

    Background:
      Given the upstream stub server "backend" returns 200 with body "real" for "GET /anything"

    Scenario: Every request that no mapping matches goes to the proxy-all target
      Given Shadow Mock is running with configuration:
        """yaml
        proxy:
          all: "${upstream.backend}"
        """
      And the global mapping:
        """json
        { "request": { "urlPath": "/mine" }, "response": { "status": 200, "body": "mapped" } }
        """
      When I send a "GET" request to "/anything"
      Then the response body equals 'real'
      When I send a "GET" request to "/mine"
      Then the response body equals 'mapped'

    Scenario: With record, each new request is written to mappings/ as soon as it has been proxied
      Given Shadow Mock is running with configuration:
        """yaml
        proxy:
          all: "${upstream.backend}"
          record: true
        """
      When I send a "GET" request to "/anything"
      Then the folder "mappings" has a file named like "get-anything-[0-9a-f-]{36}\.json"
      And a file under "__files/" contains "real"
      When I send a "GET" request to "/anything"
      Then the folder "mappings" holds 1 file

    Scenario: The proxy-all mapping has the lowest priority and no URL
      Given Shadow Mock is running with configuration:
        """yaml
        proxy:
          all: "${upstream.backend}"
        """
      When I call admin "GET" "/mappings"
      Then the response JSON at "$.mappings[0].priority" is 10
      And the response JSON at "$.mappings[0].request.method" is "ANY"
      And the response JSON at "$.mappings[0].response.proxyBaseUrl" is "${upstream.backend}"

    Scenario Outline: Targets can be denied, or allowed only by name
      Given Shadow Mock is running with configuration:
        """yaml
        proxy:
          <list>: [ "<rules>" ]
        """
      And the global mapping:
        """json
        { "request": { "urlPath": "/p" }, "response": { "proxyBaseUrl": "${upstream.backend}" } }
        """
      When I send a "GET" request to "/p"
      Then the response status is 500
      And the response body is "The target proxy address is denied in Shadow Mock's configuration."

      Examples:
        | list         | rules                |
        | denyTargets  | 127.0.0.1            |
        | denyTargets  | 127.0.0.0/8          |
        | denyTargets  | 127.0.0.1-127.0.0.9  |
        | allowTargets | *.example.com        |

    Scenario: A target that is allowed is proxied to
      Given Shadow Mock is running with configuration:
        """yaml
        proxy:
          allowTargets: [ "127.0.0.1" ]
          denyTargets: [ "10.0.0.0/8" ]
        """
      And the global mapping:
        """json
        { "request": { "urlPath": "/anything" }, "response": { "proxyBaseUrl": "${upstream.backend}" } }
        """
      When I send a "GET" request to "/anything"
      Then the response body equals 'real'

    Scenario: The client's Host header can be sent upstream
      Given Shadow Mock is running with configuration:
        """yaml
        proxy:
          preserveHostHeader: true
        """
      And the global mapping:
        """json
        { "request": { "urlPath": "/anything" }, "response": { "proxyBaseUrl": "${upstream.backend}" } }
        """
      When I send a request:
        | method | path      | header:Host        |
        | GET    | /anything | client.example:123 |
      Then the upstream "backend" received header "Host" with value "client.example:123"
