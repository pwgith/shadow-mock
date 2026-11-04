@recordings
Feature: The console's recordings
  A person can record the traffic of the global session, or of one shadow mock, through a real server and see the
  mappings it made.

  Scenario: Recording the global session
    Given I open the console
    And I open the "Recordings" tab
    Then the recording status is "NeverStarted"
    When I start recording through the upstream server
    Then the recording status is "Recording"
    When I send a "GET" request to "/claims/1"
    And I stop recording
    Then the recording result shows "Recorded 1 mapping"
    And the recording result shows "GET /claims/1"
    And the recording status is "Stopped"

  Scenario: Recording into a shadow mock
    Given a key-based shadow mock "S1"
    And I open the console
    And I open the "Recordings" tab
    When I choose the "S1 (key-based)" scope
    And I start recording through the upstream server
    Then the recording status is "Recording"
    When I send a "GET" request to "/claims/2" with header "X-Shadow-Mock-Key" set to "${S1.key}"
    And I stop recording
    Then the recording result shows "Recorded 1 mapping"
    And the recording result shows "GET /claims/2"

  Scenario: Stopping with nothing recorded
    Given I open the console
    And I open the "Recordings" tab
    And I start recording through the upstream server
    When I stop recording
    Then the recording result shows "Recorded 0 mappings"
