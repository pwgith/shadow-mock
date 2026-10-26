@wiremock @standalone @P0 @FR-1.1 @FR-1.2 @FR-1.3 @FR-1.4 @FR-1.5
Feature: Load mappings and response files from the WireMock file layout
  As a developer migrating from WireMock
  I want Shadow Mock to read my existing mappings/ and __files/ folders unchanged
  So that I can switch tools without rewriting any stubs

  Background:
    Given the mock root directory "./wiremock" is empty

  Scenario: A single mapping per file is loaded at start-up
    Given the global mappings file "mappings/get-claim.json":
      """json
      { "request": { "method": "GET", "url": "/claims/1" },
        "response": { "status": 200, "body": "claim 1" } }
      """
    And Shadow Mock is running with root "./wiremock"
    When I send a "GET" request to "/claims/1"
    Then the response status is 200
    And the response body is "claim 1"

  Scenario: A file containing several mappings is loaded
    Given the global mappings file "mappings/claims.json":
      """json
      { "mappings": [
        { "request": { "method": "GET", "url": "/claims/1" }, "response": { "status": 200, "body": "one" } },
        { "request": { "method": "GET", "url": "/claims/2" }, "response": { "status": 200, "body": "two" } } ] }
      """
    And Shadow Mock is running with root "./wiremock"
    When I send a "GET" request to "/claims/2"
    Then the response body is "two"

  Scenario: Mappings in nested folders are loaded
    Given the global mappings file "mappings/claims/v2/get.json":
      """json
      { "request": { "url": "/v2/claims" }, "response": { "status": 204 } }
      """
    And Shadow Mock is running with root "./wiremock"
    When I send a "GET" request to "/v2/claims"
    Then the response status is 204

  Scenario: bodyFileName is served from __files
    Given the file "__files/claim.json":
      """json
      { "id": 1, "status": "OPEN" }
      """
    And the global mappings file "mappings/claim.json":
      """json
      { "request": { "url": "/claims/1" },
        "response": { "status": 200, "bodyFileName": "claim.json",
                      "headers": { "Content-Type": "application/json" } } }
      """
    And Shadow Mock is running with root "./wiremock"
    When I send a "GET" request to "/claims/1"
    Then the response JSON at "$.status" is "OPEN"

  Scenario: Every supported StubMapping field is accepted
    Given the global mappings file "mappings/full.json":
      """json
      { "id": "8c5db8b0-2db4-4ad7-a99f-38c9b00da3f7", "uuid": "8c5db8b0-2db4-4ad7-a99f-38c9b00da3f7",
        "name": "full", "priority": 3, "persistent": true,
        "callResponses": [ { "calls": "1", "response": { "status": 201 } } ],
        "metadata": { "team": "claims" },
        "request": { "url": "/full" }, "response": { "status": 200 },
        "postServeActions": [], "serveEventListeners": [] }
      """
    When Shadow Mock is started with root "./wiremock"
    Then start-up succeeds with no warnings for "mappings/full.json"
    And the admin mapping "8c5db8b0-2db4-4ad7-a99f-38c9b00da3f7" has name "full"

  Scenario: Unknown fields are preserved when a mapping is read back
    Given the global mappings file "mappings/extra.json":
      """json
      { "id": "11111111-1111-1111-1111-111111111111",
        "request": { "url": "/x" }, "response": { "status": 200 },
        "vendorExtension": { "owner": "team-a" } }
      """
    And Shadow Mock is running with root "./wiremock"
    When I call admin "GET" "/mappings/11111111-1111-1111-1111-111111111111"
    Then the response JSON at "$.vendorExtension.owner" is "team-a"

  Scenario: An invalid mapping file is reported and skipped, and the others still load
    Given the global mappings file "mappings/broken.json":
      """
      { "request": { "url": "/broken"
      """
    And the global mappings file "mappings/ok.json":
      """json
      { "request": { "url": "/ok" }, "response": { "status": 200 } }
      """
    When Shadow Mock is started with root "./wiremock"
    Then the log contains an error mentioning "mappings/broken.json"
    And a "GET" request to "/ok" returns status 200

  Scenario Outline: Hot reload picks up file changes without a restart
    Given Shadow Mock is running with root "./wiremock" and hot reload "<reload>"
    When I write the global mappings file "mappings/new.json":
      """json
      { "request": { "url": "/new" }, "response": { "status": 201 } }
      """
    And I wait 500 ms
    Then a "GET" request to "/new" returns status <status>

    Examples:
      | reload   | status |
      | enabled  | 201    |
      | disabled | 404    |
