@page
Feature: The console page
  The console is a static page served by the binary. It calls only the admin API on its own origin.

  Scenario: The page loads with its five sections and nothing from another origin
    When I open the console
    Then the page header says "Shadow Mock"
    And the "Journal" tab is showing
    And the page loaded no script or style from another origin
    And the page has no script errors

  Scenario Outline: Each section can be opened
    Given I open the console
    When I open the "<tab>" tab
    Then the "<tab>" tab is showing
    And the page has no script errors

    Examples:
      | tab          |
      | Shadow mocks |
      | Counters     |
      | Recordings   |
      | Dry run      |
      | Journal      |
