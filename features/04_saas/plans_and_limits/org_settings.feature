@managed @P5 @FR-3.5a @FR-11.11 @NFR-4.4
Feature: An org's own settings for compression and the journal
  The management app writes an org's settings into its record. The engine applies them within a second, and the
  defaults are the engine's own: everything journalled, with bodies, and responses compressed.

  Background:
    Given Shadow Mock is running in SaaS mode
    And the org "acme" with handle "acme"
    And org "acme" has the global mapping:
      """json
      { "request": { "urlPath": "/big" }, "response": { "status": 200, "body": "${text:4096}", "headers": { "Content-Type": "application/json" } } }
      """

  Scenario: An org can turn compression off
    Given the org "acme" has setting "gzip" set to "off"
    When I send a request:
      | method | path      | header:Accept-Encoding |
      | GET    | /acme/big | gzip                   |
    Then the response status is 200
    And the response header "Content-Encoding" is absent

  Scenario: Compression is on until an org turns it off
    When I send a request:
      | method | path      | header:Accept-Encoding |
      | GET    | /acme/big | gzip                   |
    Then the response status is 200
    And the response header "Content-Encoding" is "gzip"
