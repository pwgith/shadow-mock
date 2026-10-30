@saas @management-app @P5 @FR-5.6 @FR-5.3
Feature: Browsing journal history in the management app

  Background:
    Given the org "acme" with handle "acme" on plan "Team"
    And I am signed in to the management app as "erin" in org "acme" with app role "member"
    And org "acme" has 500 journal entries from the last 3 days

  Scenario: Search the journal by summary fields
    When I search the journal for status "404" and path containing "/claims"
    Then I see only matching entries, newest first
    And the search used only the journal table, without reading object storage

  Scenario: Open an entry with an inline body
    When I open an entry whose request body is 1 KB
    Then I see the full request and response

  Scenario: Open an entry whose body is in a shared object
    When I open an entry whose request body is 20 KB
    Then the app fetches only that entry's byte range from object storage
    And I see the full 20 KB body

  Scenario: Near-miss analysis for an unmatched entry
    When I open an unmatched entry and ask for near misses
    Then the app calls the engine and shows the closest mappings by layer

  Scenario: Export a session's journal
    Given a key-based shadow mock "S1" with 50 journal entries
    When I export the journal of "S1"
    Then I receive a download link that expires after 60 s

  Scenario: Entries older than the plan's retention are gone
    Given an entry is 31 days old on the Team plan
    When I search for it
    Then it is not found
