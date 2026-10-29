@wiremock @P3 @FR-3.5a
Feature: Gzip response compression
  Responses are gzip-compressed when the client accepts it, as WireMock does
  by default. This is also the main lever on SaaS bandwidth cost.

  Background:
    Given Shadow Mock is running
    And the global mapping:
      """json
      { "request": { "urlPath": "/big" }, "response": { "status": 200, "body": "${text:4096}", "headers": { "Content-Type": "application/json" } } }
      """

  Scenario Outline: Compression follows Accept-Encoding
    When I send a request:
      | method | path | header:Accept-Encoding |
      | GET    | /big | <accept>               |
    Then the response header "Content-Encoding" is "<encoding>"
    And the decoded response body has 4096 characters

    Examples:
      | accept        | encoding |
      | gzip          | gzip     |
      | gzip, deflate | gzip     |
      | identity      | absent   |

  Scenario: A mapping can opt out of compression
    Given the global mapping:
      """json
      { "request": { "urlPath": "/nogzip" }, "response": { "status": 200, "body": "${text:4096}", "gzipDisabled": true } }
      """
    When I send a request:
      | method | path    | header:Accept-Encoding |
      | GET    | /nogzip | gzip                   |
    Then the response header "Content-Encoding" is absent

  @FR-10.10
  Scenario: A small body is compressed too, and no Vary header is added
    Given the global mapping:
      """json
      { "request": { "urlPath": "/tiny" }, "response": { "status": 200, "body": "ok" } }
      """
    When I send a request:
      | method | path  | header:Accept-Encoding |
      | GET    | /tiny | gzip                   |
    Then the response header "Content-Encoding" is "gzip"
    And the response header "Vary" is absent
    And the decoded response body has 2 characters

  Scenario: A body that is already encoded is not compressed again
    Given the global mapping:
      """json
      { "request": { "urlPath": "/br" }, "response": { "status": 200, "body": "ok", "headers": { "Content-Encoding": "br" } } }
      """
    When I send a request:
      | method | path | header:Accept-Encoding |
      | GET    | /br  | gzip                   |
    Then the response header "Content-Encoding" is "br"

  @saas
  Scenario: An org can turn compression off
    Given the org "acme" has setting "gzip" set to "off"
    When I send a request:
      | method | path | header:Accept-Encoding |
      | GET    | /big | gzip                   |
    Then the response header "Content-Encoding" is absent
