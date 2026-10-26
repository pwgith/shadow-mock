@saas @management-app @P5 @FR-9.5a @FR-9.8
Feature: Managing the org's global session in the management app
  The only write path for global mappings and files. The app checks team
  grants and validates mappings with the engine before writing to the
  database.

  Background:
    Given the org "acme" with handle "acme" on plan "Team"
    And the team "platform-qa" in org "acme" has the Shadow Mock grant "global:write"
    And "dana" is a member of team "platform-qa"
    And "erin" is an org member in no team

  Scenario Outline: Who can change global mappings
    Given I am signed in to the management app as "<user>" in org "acme"
    When I create the global mapping "GET /quotes → 200"
    Then the result is "<result>"

    Examples:
      | user  | result                        |
      | dana  | saved                         |
      | erin  | forbidden: needs global:write |
      | carol | saved                         |

  Scenario: Everyone in the org can read global mappings
    Given I am signed in to the management app as "erin" in org "acme"
    When I open the global mappings page
    Then I see the org's global mappings

  Scenario: Mappings are validated by the engine before saving
    Given I am signed in to the management app as "dana" in org "acme"
    When I save a global mapping with both "url" and "urlPath"
    Then the save is rejected with the engine's validation message "only one of url, urlPath"
    And nothing is written to the database

  Scenario: Saved mappings follow the shared JSON schema and keep unknown fields
    Given I am signed in to the management app as "dana" in org "acme"
    When I save a global mapping containing the extra field "vendorExtension"
    Then the stored mapping body still contains "vendorExtension"

  Scenario: Upload a response file
    Given I am signed in to the management app as "dana" in org "acme"
    When I upload "quote.json" as a global response file
    Then the file goes directly from the browser to object storage through a presigned URL
    And a files row records its name, size, content type and hash

  Scenario: Team grants are managed by org admins only
    Given I am signed in to the management app as "dana" in org "acme"
    When I try to grant "global:write" to team "claims-squad"
    Then the result is "forbidden: org admins only"

  Scenario: Database row-level security limits the app to the signed-in org
    Given I am signed in to the management app as "dana" in org "acme"
    When the management app queries the mappings table without an org filter
    Then only rows for org "acme" are returned
