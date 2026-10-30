@saas @management-app @P5 @FR-9.6 @FR-9.6b @FR-5.6
Feature: Org settings in the management app
  Org admins manage the handle, the IP allowlist, journal settings and
  compression. Changes reach the engine within a second.

  Background:
    Given the org "acme" with handle "acme" on plan "Team"
    And I am signed in to the management app as "carol" in org "acme" with app role "admin"

  Scenario: The settings page shows the engine base URL built from the handle
    When I open the org settings page
    Then I see the engine base URL "https://shadowmock.kaizentis.xyz/acme"

  Scenario: Change the handle
    When I change the handle to "acme-insurance"
    Then I see the new base URL "https://shadowmock.kaizentis.xyz/acme-insurance"
    And I am told the old URL keeps working as an alias

  Scenario: Manage the IP allowlist with labels
    When I add the allowlist entry "203.0.113.0/24" labelled "Test env NAT"
    And I add the allowlist entry "2001:db8::/48" labelled "IPv6 lab"
    Then the allowlist shows 2 entries with their labels

  Scenario Outline: Invalid allowlist entries are rejected
    When I add the allowlist entry "<cidr>"
    Then I see the error "<error>"

    Examples:
      | cidr            | error              |
      | 203.0.113.0/33  | invalid CIDR       |
      | not-an-ip       | invalid CIDR       |
      | 203.0.113.0/24  | already in the list |

  Scenario: Warn before locking yourself out of the admin API
    Given my current IP is "198.51.100.20"
    When I turn on "applyToAdmin" with an allowlist that does not include "198.51.100.20"
    Then I am warned that I will lose admin access from this IP
    And the change is only saved if I confirm

  Scenario: The allowlist entry limit follows the plan
    Given the org has 100 allowlist entries
    When I add another allowlist entry
    Then I see the error "allowlist limit reached"

  Scenario: Journal and compression settings
    When I set journal capture to "sessions-and-unmatched"
    And I turn journal body storage off
    And I turn response compression off
    Then within 1 s the engine applies all three settings for org "acme"

  Scenario: Members cannot change org settings
    Given I am signed in to the management app as "erin" in org "acme" with app role "member"
    When I try to change the handle
    Then the result is "forbidden: org admins only"
