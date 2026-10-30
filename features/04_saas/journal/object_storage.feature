@saas @P5 @NFR-3.1 @security
Feature: Object storage for files, exports and journal bodies

  Background:
    Given Shadow Mock is running in SaaS mode
    And the org "acme" with handle "acme" on plan "Team"

  Scenario: __files uploaded by the management app are served by the engine
    Given the management app uploads "claim.json" for org "acme" through a presigned URL
    And org "acme" has the global mapping:
      """json
      { "request": { "urlPath": "/claim" }, "response": { "status": 200, "bodyFileName": "claim.json" } }
      """
    When I send a "GET" request to "/claim"
    Then the response body equals the uploaded "claim.json"

  Scenario: Files are content-addressed under the org's prefix
    When the management app uploads "claim.json" for org "acme"
    Then the object key starts with "files/<acme org id>/"
    And the files row maps "claim.json" to that key

  Scenario: Replacing a file is picked up by the engine cache
    Given the engine has served "claim.json" for org "acme"
    When the management app replaces "claim.json" with new content
    Then within 1 s the engine serves the new content

  Scenario: Presigned URLs expire
    Given the management app issued a presigned download URL for an export
    When the test clock advances by "61s"
    Then the presigned URL is rejected by object storage

  Scenario: The bucket has no public access
    When an anonymous client lists or reads the bucket
    Then access is denied

  Scenario: Exports are written under the org's prefix
    Given I am "alice" in org "acme" with app role "member"
    When I request a journal export for session "S1"
    Then an object is written under "exports/<acme org id>/"
