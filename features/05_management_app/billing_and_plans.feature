@saas @management-app @P5 @NFR-4.3
Feature: Plans, entitlements and billing across the platform
  The bill is the platform org fee plus each app's tier and overages. Plan
  changes reach the engine as entitlements.

  Background:
    Given the org "acme" exists on the platform

  Scenario Outline: Example monthly bills
    Given the org "acme" is on platform plan "<platform>" and Shadow Mock plan "<app>"
    And this month org "acme" used <requests> mock requests and <sessions> p95 concurrent sessions
    When the month's invoice is calculated
    Then the total is <total>

    Examples:
      | platform | app      | requests | sessions | total   |
      | Free     | Free     | 80000    | 1        | $0      |
      | Team     | Starter  | 1500000  | 8        | $48     |
      | Team     | Team     | 20000000 | 60       | $148    |
      | Business | Business | 60000000 | 300      | $568    |

  Scenario: A Free platform org can only use Free app tiers
    Given the org "acme" is on platform plan "Free"
    When an org admin tries to choose Shadow Mock plan "Starter"
    Then they are asked to upgrade the platform plan first

  Scenario: An upgrade reaches the engine as new limits
    Given the org "acme" is on Shadow Mock plan "Starter"
    When the org admin upgrades to Shadow Mock plan "Team"
    Then platform.subscriptions holds plan "Team" for app "shadowmock"
    And within 1 s orgs.limits for "acme" shows the Team limits

  Scenario: Inactive free orgs are archived after 90 days
    Given the org "acme" is on platform plan "Free" with no activity for 91 days
    When the archive job runs
    Then the org "acme" is archived and no longer counts as a retained Clerk org

  Scenario: Annual billing gets two months free
    Given the org "acme" chooses annual billing for Shadow Mock plan "Team"
    Then the annual price is 10 × $99
