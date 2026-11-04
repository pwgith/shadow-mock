@sessions
Feature: The console's shadow mocks
  The Shadow mocks section lists the shadow mocks, and lets a person edit, keep alive and delete them.

  Scenario: An empty list says so
    Given I open the console
    When I open the "Shadow mocks" tab
    Then the console says there are no shadow mocks

  Scenario: Key-based and URL-based shadow mocks are listed
    Given a key-based shadow mock "S1"
    And a URL-based shadow mock "U1"
    And I open the console
    When I open the "Shadow mocks" tab
    Then the console lists the shadow mock "S1"
    And the shadow mock "S1" is shown as "key-based"
    And the console lists the shadow mock "U1"
    And the shadow mock "U1" is shown as "url-based"

  Scenario: A shadow mock that does not fall back to the global session is marked
    Given a key-based shadow mock "Strict" with:
      """json
      { "inheritGlobal": false }
      """
    And a key-based shadow mock "Normal"
    And I open the console
    When I open the "Shadow mocks" tab
    Then the shadow mock "Strict" is shown as "no global fallback"
    And the shadow mock "Normal" is not marked as having no global fallback

  Scenario: Deleting a shadow mock asks first, then deletes it
    Given a key-based shadow mock "S1"
    And a key-based shadow mock "S2"
    And I open the console
    And I open the "Shadow mocks" tab
    When I delete the shadow mock "S1"
    Then I was asked "Delete S1?"
    And the console does not list the shadow mock "S1"
    And the console lists the shadow mock "S2"
    And the admin API no longer has the shadow mock "S1"
    And the admin API still has the shadow mock "S2"

  Scenario: Declining the question deletes nothing
    Given a key-based shadow mock "S1"
    And I will decline the confirmation
    And I open the console
    And I open the "Shadow mocks" tab
    When I delete the shadow mock "S1"
    Then I was asked "Delete S1?"
    And the console lists the shadow mock "S1"
    And the admin API still has the shadow mock "S1"

  Scenario: Renaming and changing the lifetime
    Given a key-based shadow mock "S1" with:
      """json
      { "ttlSeconds": 600 }
      """
    And I open the console
    And I open the "Shadow mocks" tab
    When I edit the shadow mock "S1"
    And I rename it to "Renamed"
    Then the admin API shows the shadow mock "S1" with name "Renamed"
    When I set its lifetime to 7200 seconds
    Then the admin API shows the shadow mock "S1" living about 7200 seconds

  Scenario: Saving with nothing changed says so
    Given a key-based shadow mock "S1"
    And I open the console
    And I open the "Shadow mocks" tab
    And I edit the shadow mock "S1"
    When I save without changing anything
    Then the status line says "Nothing to save."

  Scenario: Adding and removing a match token
    Given a key-based shadow mock "S1"
    And I open the console
    And I open the "Shadow mocks" tab
    And I edit the shadow mock "S1"
    When I add the match token "POL-555"
    Then the admin API shows the shadow mock "S1" with token "POL-555"
    When I remove the match token "POL-555"
    Then the admin API shows the shadow mock "S1" without token "POL-555"

  Scenario: A URL-based shadow mock has no match tokens
    Given a URL-based shadow mock "U1"
    And I open the console
    And I open the "Shadow mocks" tab
    When I edit the shadow mock "U1"
    Then the editor offers no match tokens

  Scenario: Hiding a mapping of a lower layer from a shadow mock
    Given the global mapping:
      """json
      { "id": "aaaaaaaa-0000-0000-0000-000000000001", "request": { "urlPath": "/fx" }, "response": { "status": 200 } }
      """
    And a key-based shadow mock "S1"
    And I open the console
    And I open the "Shadow mocks" tab
    And I edit the shadow mock "S1"
    When I hide the mapping with id "aaaaaaaa-0000-0000-0000-000000000001"
    Then the admin API shows the shadow mock "S1" hiding the mapping "aaaaaaaa-0000-0000-0000-000000000001"
    When I stop hiding the mapping with id "aaaaaaaa-0000-0000-0000-000000000001"
    Then the editor shows "None"

  Scenario: Keeping a shadow mock alive
    Given a key-based shadow mock "S1" with:
      """json
      { "ttlSeconds": 600 }
      """
    And I open the console
    And I open the "Shadow mocks" tab
    And I edit the shadow mock "S1"
    When I keep it alive
    Then the status line shows no error
    And the admin API shows the shadow mock "S1" living about 600 seconds

  Scenario: The editor can be closed
    Given a key-based shadow mock "S1"
    And I open the console
    And I open the "Shadow mocks" tab
    And I edit the shadow mock "S1"
    When I close the editor
    Then the editor is closed
