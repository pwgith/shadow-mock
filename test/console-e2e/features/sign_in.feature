@signin
Feature: Signing in to the console of a central server
  A central server asks for an admin token on every admin call. The console has a token field and sends what is in it.

  Background:
    Given the central server asks for an admin token

  Scenario: Without a token the console shows the refusal
    When I open the console
    Then the status line says "401"

  Scenario: With the token the console shows what the server holds
    Given a key-based shadow mock "S1"
    And I open the console
    When I enter the admin token
    And I open the "Shadow mocks" tab
    Then the console lists the shadow mock "S1"
    And the status line shows no error

  Scenario: The token field does not show what is typed
    When I open the console
    Then the admin token field hides what is typed
